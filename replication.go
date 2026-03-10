package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/robotelu/db_barrel_2.0/internal/api"
	"github.com/robotelu/db_barrel_2.0/internal/config"
)

type pgEndpoint struct {
	Name     string
	Host     string
	Port     int
	Database string
	DSN      string
}

type endpointIndex struct {
	byHostPortDB map[string][]string
	byHostPort   map[string][]string
	byHost       map[string][]string
	byDB         map[string][]string
	byName       map[string][]string
}

type pgPrimaryClient struct {
	Hostname        string
	Address         string
	ApplicationName string
}

type pgLogicalSubscription struct {
	Name      string
	ConnInfo  string
	TablesCSV string
}

type pgEndpointObservation struct {
	Endpoint             pgEndpoint
	Report               api.ReplicationEndpointReport
	ServerAddress        string
	ServerPort           int
	SystemIdentifier     string
	StandbyConnFields    []map[string]string
	StandbyProbe         standbyProbeReport
	PrimaryClients       []pgPrimaryClient
	LogicalSubscriptions []pgLogicalSubscription
}

type standbySourceResolution struct {
	SourceName string
	Note       string
}

func buildReplication(cfg *config.Config) []api.ReplicationInfo {
	links, _ := buildReplicationWithReport(cfg)
	return links
}

func buildReplicationWithReport(cfg *config.Config) ([]api.ReplicationInfo, api.ReplicationReport) {
	auto, endpointReports := discoverPostgresReplicationWithReport(cfg)
	manual := manualReplicationLinks(cfg)
	combined := append(append(make([]api.ReplicationInfo, 0, len(auto)+len(manual)), auto...), manual...)
	finalLinks, dropped := dedupeReplicationLinksWithDropped(combined)

	endpointErrors := 0
	for _, ep := range endpointReports {
		endpointErrors += len(ep.Errors)
	}

	report := api.ReplicationReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Summary: api.ReplicationSummary{
			ConfiguredDatabases:         len(cfg.Databases),
			ConfiguredPostgresDatabases: len(postgresEndpoints(cfg)),
			AutoDiscoveredLinks:         len(auto),
			MergedLinks:                 len(finalLinks),
			DroppedLinks:                len(dropped),
			EndpointErrors:              endpointErrors,
		},
		PostgresEndpoints: endpointReports,
		DroppedLinks:      dropped,
		FinalLinks:        append([]api.ReplicationInfo(nil), finalLinks...),
	}

	log.Printf("🔗 Replication summary: postgres_endpoints=%d auto_links=%d endpoint_errors=%d",
		report.Summary.ConfiguredPostgresDatabases,
		report.Summary.AutoDiscoveredLinks,
		report.Summary.EndpointErrors,
	)

	for _, ep := range endpointReports {
		if len(ep.Errors) == 0 {
			continue
		}
		log.Printf("  ⚠️ Replication endpoint %q (%s:%d/%s) errors: %s", ep.Name, ep.Host, ep.Port, ep.Database, strings.Join(ep.Errors, " | "))
	}
	if len(finalLinks) == 0 {
		log.Printf("  ⚠️ Replication produced zero links. Check /api/topology/report for diagnostics.")
	}

	return finalLinks, report
}

func discoverPostgresReplication(cfg *config.Config) []api.ReplicationInfo {
	links, _ := discoverPostgresReplicationWithReport(cfg)
	return links
}

func discoverPostgresReplicationWithReport(cfg *config.Config) ([]api.ReplicationInfo, []api.ReplicationEndpointReport) {
	endpoints := postgresEndpoints(cfg)
	if len(endpoints) == 0 {
		return nil, nil
	}

	observations := make([]pgEndpointObservation, 0, len(endpoints))
	for _, ep := range endpoints {
		observations = append(observations, inspectPostgresEndpoint(ep))
	}

	idx := buildObservedEndpointIndex(observations)
	links := make([]api.ReplicationInfo, 0)
	reports := make([]api.ReplicationEndpointReport, 0, len(observations))
	reportIdx := make(map[string]int, len(observations))
	primarySet := make(map[string]bool, len(observations))
	systemPrimaries := make(map[string][]string)

	for _, obs := range observations {
		reports = append(reports, obs.Report)
		reportIdx[obs.Endpoint.Name] = len(reports) - 1
		if obs.Report.InRecoveryKnown && !obs.Report.InRecovery {
			primarySet[obs.Endpoint.Name] = true
			if obs.SystemIdentifier != "" {
				systemPrimaries[obs.SystemIdentifier] = appendUnique(systemPrimaries[obs.SystemIdentifier], obs.Endpoint.Name)
			}
		}
	}

	primaryNames := mapKeys(primarySet)
	for _, obs := range observations {
		if obs.Report.InRecoveryKnown && obs.Report.InRecovery {
			resolution, ok := resolveStandbySource(obs, idx, primarySet, primaryNames, systemPrimaries)
			if ok && resolution.SourceName != obs.Endpoint.Name {
				links = append(links, api.ReplicationInfo{
					SourceName: resolution.SourceName,
					TargetName: obs.Endpoint.Name,
					Type:       "streaming",
				})
				if idx, ok := reportIdx[obs.Endpoint.Name]; ok {
					reports[idx].StreamingLinksDetected++
					if resolution.Note != "" {
						reports[idx].Notes = append(reports[idx].Notes, resolution.Note)
					}
				}
			}
			continue
		}

		if !obs.Report.InRecoveryKnown {
			continue
		}

		for _, client := range obs.PrimaryClients {
			targetName, ok := resolvePrimaryClientTarget(client, idx)
			if !ok || targetName == obs.Endpoint.Name {
				continue
			}
			links = append(links, api.ReplicationInfo{
				SourceName: obs.Endpoint.Name,
				TargetName: targetName,
				Type:       "streaming",
			})
			if idx, ok := reportIdx[obs.Endpoint.Name]; ok {
				reports[idx].StreamingLinksDetected++
			}
		}
	}

	for _, obs := range observations {
		for _, sub := range obs.LogicalSubscriptions {
			if idx, ok := reportIdx[obs.Endpoint.Name]; ok {
				reports[idx].LogicalSubscriptionsScanned++
			}

			fields := parsePGConnInfo(sub.ConnInfo)
			sourceName, ok := matchSourceEndpoint(fields, idx)
			if !ok || sourceName == obs.Endpoint.Name {
				if idx, ok := reportIdx[obs.Endpoint.Name]; ok {
					reports[idx].LogicalSubscriptionsUnmatched++
				}
				continue
			}

			links = append(links, api.ReplicationInfo{
				SourceName: sourceName,
				TargetName: obs.Endpoint.Name,
				Type:       "logical",
				Details:    logicalDetails(sub.Name, sub.TablesCSV),
			})
			if idx, ok := reportIdx[obs.Endpoint.Name]; ok {
				reports[idx].LogicalSubscriptionsMatched++
			}
		}
	}

	finalLinks, _ := dedupeReplicationLinksWithDropped(links)
	return finalLinks, reports
}

type standbyProbeReport struct {
	PrimaryConnInfoSeen     bool
	WalReceiverConnInfoSeen bool
}

func inspectPostgresEndpoint(ep pgEndpoint) pgEndpointObservation {
	obs := pgEndpointObservation{
		Endpoint: ep,
		Report: api.ReplicationEndpointReport{
			Name:     ep.Name,
			Host:     ep.Host,
			Port:     ep.Port,
			Database: ep.Database,
		},
	}

	db, err := sql.Open("postgres", ep.DSN)
	if err != nil {
		log.Printf("  ⚠️ [%s] replication discover: open failed: %v", ep.Name, err)
		obs.Report.Errors = append(obs.Report.Errors, "open failed: "+err.Error())
		return obs
	}
	defer db.Close()
	obs.Report.ConnectOK = true

	if err := db.Ping(); err != nil {
		log.Printf("  ⚠️ [%s] replication discover: ping failed: %v", ep.Name, err)
		obs.Report.Errors = append(obs.Report.Errors, "ping failed: "+err.Error())
		return obs
	}
	obs.Report.PingOK = true

	obs.Report.InRecovery, obs.Report.InRecoveryKnown = queryBool(db, "SELECT pg_is_in_recovery()")
	if !obs.Report.InRecoveryKnown {
		obs.Report.Errors = append(obs.Report.Errors, "pg_is_in_recovery query failed")
	}

	if addr, ok := queryString(db, "SELECT COALESCE(inet_server_addr()::text, '')"); ok {
		obs.ServerAddress = strings.TrimSpace(addr)
	}
	if port, ok := queryInt(db, "SELECT inet_server_port()"); ok {
		obs.ServerPort = port
	}
	if systemID, ok := queryString(db, "SELECT COALESCE(system_identifier::text, '') FROM pg_control_system()"); ok && strings.TrimSpace(systemID) != "" {
		obs.SystemIdentifier = strings.TrimSpace(systemID)
	} else {
		obs.Report.Notes = append(obs.Report.Notes, "pg_control_system query failed or unavailable")
	}

	if obs.Report.InRecoveryKnown && obs.Report.InRecovery {
		obs.StandbyConnFields, obs.StandbyProbe = collectStandbyConnFields(db)
		obs.Report.PrimaryConnInfoSeen = obs.StandbyProbe.PrimaryConnInfoSeen
		obs.Report.WalReceiverConnInfoSeen = obs.StandbyProbe.WalReceiverConnInfoSeen
	} else if obs.Report.InRecoveryKnown {
		obs.PrimaryClients = collectPrimaryClients(db)
	}

	obs.LogicalSubscriptions = collectLogicalSubscriptions(db)

	return obs
}

func collectStandbyConnFields(db *sql.DB) ([]map[string]string, standbyProbeReport) {
	probe := standbyProbeReport{}
	connInfoCandidates := make([]string, 0, 2)

	// primary_conninfo is often available on standbys.
	if primaryConnInfo, ok := queryString(db, "SELECT COALESCE(current_setting('primary_conninfo', true), '')"); ok && strings.TrimSpace(primaryConnInfo) != "" {
		probe.PrimaryConnInfoSeen = true
		connInfoCandidates = append(connInfoCandidates, primaryConnInfo)
	}

	// Fallback for setups where current_setting is unavailable/restricted.
	if walConnInfo, ok := queryString(db, "SELECT COALESCE(conninfo, '') FROM pg_stat_wal_receiver LIMIT 1"); ok && strings.TrimSpace(walConnInfo) != "" {
		probe.WalReceiverConnInfoSeen = true
		connInfoCandidates = append(connInfoCandidates, walConnInfo)
	}

	connFields := make([]map[string]string, 0, len(connInfoCandidates))
	for _, connInfo := range connInfoCandidates {
		connFields = append(connFields, parsePGConnInfo(connInfo))
	}

	return connFields, probe
}

func inferStreamingSource(connFields []map[string]string, idx endpointIndex, primarySet map[string]bool, primaryNames []string) (string, bool) {
	hadCandidates := false

	for _, fields := range connFields {
		hosts := parseConnHosts(fields)
		if len(hosts) == 0 {
			continue
		}

		dbName := fields["dbname"]
		ports := parseConnPorts(fields["port"], len(hosts), 5432)

		for i, host := range hosts {
			port := ports[min(i, len(ports)-1)]
			candidates := idx.candidatesExact(host, port, dbName)
			if len(candidates) > 0 {
				hadCandidates = true
			}
			if sourceName, ok := chooseCandidate(candidates, primarySet, primaryNames); ok {
				return sourceName, true
			}
		}

		for i, host := range hosts {
			port := ports[min(i, len(ports)-1)]
			candidates := idx.candidatesHostPort(host, port)
			if len(candidates) > 0 {
				hadCandidates = true
			}
			if sourceName, ok := chooseCandidate(candidates, primarySet, primaryNames); ok {
				return sourceName, true
			}
		}

		dbCandidates := idx.byDB[normalizeDB(dbName)]
		if len(dbCandidates) > 0 {
			hadCandidates = true
		}
		if sourceName, ok := chooseCandidate(dbCandidates, primarySet, primaryNames); ok {
			return sourceName, true
		}
	}

	// Last-resort heuristic: one known primary and at least one host/port overlap candidate seen.
	if hadCandidates && len(primaryNames) == 1 {
		return primaryNames[0], true
	}
	return "", false
}

func chooseCandidate(candidates []string, primarySet map[string]bool, primaryNames []string) (string, bool) {
	if len(candidates) == 0 {
		return "", false
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}

	primaryCandidates := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if primarySet[c] {
			primaryCandidates = appendUnique(primaryCandidates, c)
		}
	}

	if len(primaryCandidates) == 1 {
		return primaryCandidates[0], true
	}

	if len(primaryNames) == 1 {
		only := primaryNames[0]
		for _, c := range candidates {
			if c == only {
				return only, true
			}
		}
	}

	return "", false
}

func resolveStandbySource(obs pgEndpointObservation, idx endpointIndex, primarySet map[string]bool, primaryNames []string, systemPrimaries map[string][]string) (standbySourceResolution, bool) {
	if sourceName, ok := inferStreamingSource(obs.StandbyConnFields, idx, primarySet, primaryNames); ok {
		return standbySourceResolution{
			SourceName: sourceName,
			Note:       "streaming source matched from standby connection info",
		}, true
	}

	if obs.SystemIdentifier != "" {
		candidates := make([]string, 0)
		for _, candidate := range systemPrimaries[obs.SystemIdentifier] {
			if candidate != obs.Endpoint.Name {
				candidates = appendUnique(candidates, candidate)
			}
		}
		if len(candidates) == 1 {
			return standbySourceResolution{
				SourceName: candidates[0],
				Note:       "streaming source inferred from PostgreSQL system identifier",
			}, true
		}
	}

	return standbySourceResolution{}, false
}

func resolvePrimaryClientTarget(client pgPrimaryClient, idx endpointIndex) (string, bool) {
	for _, host := range []string{client.Hostname, client.Address} {
		if targetName, ok := idx.findByHost(host); ok {
			return targetName, true
		}
	}
	if targetName, ok := idx.findByName(client.ApplicationName); ok {
		return targetName, true
	}
	return "", false
}

func matchSourceEndpoint(fields map[string]string, idx endpointIndex) (string, bool) {
	hosts := parseConnHosts(fields)
	if len(hosts) == 0 {
		return "", false
	}

	dbName := fields["dbname"]
	ports := parseConnPorts(fields["port"], len(hosts), 5432)

	// Prefer exact host:port:dbname when available.
	for i, host := range hosts {
		port := ports[i]
		if sourceName, ok := idx.find(host, port, dbName); ok {
			return sourceName, true
		}
	}

	// Fallback to host:port unique match.
	for i, host := range hosts {
		port := ports[i]
		if sourceName, ok := idx.find(host, port, ""); ok {
			return sourceName, true
		}
	}

	return "", false
}

func parseConnHosts(fields map[string]string) []string {
	return splitCSVTrim(firstNonEmpty(fields["host"], fields["hostaddr"]))
}

func parseConnPorts(portValue string, hostsCount int, fallback int) []int {
	raw := splitCSVTrim(portValue)
	if len(raw) == 0 {
		raw = []string{strconv.Itoa(fallback)}
	}

	ports := make([]int, 0, max(hostsCount, len(raw)))
	for _, v := range raw {
		ports = append(ports, parsePortDefault(v, fallback))
	}

	if hostsCount > 1 && len(ports) == 1 {
		// Single port applies to all hosts.
		for len(ports) < hostsCount {
			ports = append(ports, ports[0])
		}
	}

	return ports
}

func logicalDetails(subName, tablesCSV string) string {
	tables := splitCSVTrim(tablesCSV)
	if len(tables) == 0 {
		if subName == "" {
			return "tables: unknown"
		}
		return "subscription: " + subName
	}

	maxTables := 4
	visible := tables
	suffix := ""
	if len(tables) > maxTables {
		visible = tables[:maxTables]
		suffix = fmt.Sprintf(" +%d", len(tables)-maxTables)
	}

	details := "tables: " + strings.Join(visible, ", ") + suffix
	if subName == "" {
		return details
	}
	return subName + " | " + details
}

func manualReplicationLinks(cfg *config.Config) []api.ReplicationInfo {
	out := make([]api.ReplicationInfo, 0, len(cfg.Replication))
	for _, link := range cfg.Replication {
		linkType := strings.TrimSpace(link.Type)
		if linkType == "" {
			linkType = "streaming"
		}
		out = append(out, api.ReplicationInfo{
			SourceName: strings.TrimSpace(link.SourceName),
			TargetName: strings.TrimSpace(link.TargetName),
			Type:       linkType,
			Details:    strings.TrimSpace(link.Details),
		})
	}
	return out
}

func postgresEndpoints(cfg *config.Config) []pgEndpoint {
	out := make([]pgEndpoint, 0)
	for _, db := range cfg.Databases {
		if strings.ToLower(db.Driver) != "postgresql" {
			continue
		}
		port := db.Port
		if port == 0 {
			port = 5432
		}
		out = append(out, pgEndpoint{
			Name:     db.Name,
			Host:     db.Host,
			Port:     port,
			Database: db.Database,
			DSN:      db.BuildDSN(),
		})
	}
	return out
}

func buildEndpointIndex(endpoints []pgEndpoint) endpointIndex {
	idx := endpointIndex{
		byHostPortDB: make(map[string][]string, len(endpoints)),
		byHostPort:   make(map[string][]string, len(endpoints)),
		byHost:       make(map[string][]string, len(endpoints)),
		byDB:         make(map[string][]string, len(endpoints)),
		byName:       make(map[string][]string, len(endpoints)),
	}
	for _, ep := range endpoints {
		addEndpointToIndex(&idx, ep.Name, ep.Database, ep.Port, ep.Host)
	}
	return idx
}

func buildObservedEndpointIndex(observations []pgEndpointObservation) endpointIndex {
	idx := endpointIndex{
		byHostPortDB: make(map[string][]string, len(observations)),
		byHostPort:   make(map[string][]string, len(observations)),
		byHost:       make(map[string][]string, len(observations)),
		byDB:         make(map[string][]string, len(observations)),
		byName:       make(map[string][]string, len(observations)),
	}

	for _, obs := range observations {
		addEndpointToIndex(&idx, obs.Endpoint.Name, obs.Endpoint.Database, obs.Endpoint.Port, obs.Endpoint.Host)
		if obs.ServerAddress != "" {
			port := obs.ServerPort
			if port == 0 {
				port = obs.Endpoint.Port
			}
			addEndpointToIndex(&idx, obs.Endpoint.Name, obs.Endpoint.Database, port, obs.ServerAddress)
		}
	}

	return idx
}

func addEndpointToIndex(idx *endpointIndex, name, database string, port int, hosts ...string) {
	nameKey := strings.ToLower(strings.TrimSpace(name))
	if nameKey != "" {
		idx.byName[nameKey] = appendUnique(idx.byName[nameKey], name)
	}

	dbKey := normalizeDB(database)
	if dbKey != "" {
		idx.byDB[dbKey] = appendUnique(idx.byDB[dbKey], name)
	}

	for _, rawHost := range hosts {
		for _, host := range canonicalHostVariants(rawHost) {
			hp := hostPortKey(host, port)
			hpd := hostPortDBKey(host, port, dbKey)
			idx.byHostPortDB[hpd] = appendUnique(idx.byHostPortDB[hpd], name)
			idx.byHostPort[hp] = appendUnique(idx.byHostPort[hp], name)
			idx.byHost[host] = appendUnique(idx.byHost[host], name)
		}
	}
}

func (i endpointIndex) find(host string, port int, database string) (string, bool) {
	dbKey := normalizeDB(database)
	if dbKey != "" {
		candidates := i.candidatesExact(host, port, dbKey)
		if len(candidates) == 1 {
			return candidates[0], true
		}
	}

	candidates := i.candidatesHostPort(host, port)
	if len(candidates) == 1 {
		return candidates[0], true
	}

	// Last fallback when host cannot be mapped but dbname is unique in config.
	if dbKey != "" {
		dbCandidates := i.byDB[dbKey]
		if len(dbCandidates) == 1 {
			return dbCandidates[0], true
		}
	}

	return "", false
}

func (i endpointIndex) findByHost(host string) (string, bool) {
	candidates := i.candidatesByHost(host)
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return "", false
}

func (i endpointIndex) findByName(name string) (string, bool) {
	candidates := i.byName[strings.ToLower(strings.TrimSpace(name))]
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return "", false
}

func (i endpointIndex) candidatesExact(host string, port int, database string) []string {
	database = normalizeDB(database)
	if database == "" {
		return nil
	}
	out := make([]string, 0)
	for _, variant := range canonicalHostVariants(host) {
		if variant == "" {
			continue
		}

		for _, c := range i.byHostPortDB[hostPortDBKey(variant, port, database)] {
			out = appendUnique(out, c)
		}
	}
	return out
}

func (i endpointIndex) candidatesHostPort(host string, port int) []string {
	out := make([]string, 0)
	for _, variant := range canonicalHostVariants(host) {
		if variant == "" {
			continue
		}

		for _, c := range i.byHostPort[hostPortKey(variant, port)] {
			out = appendUnique(out, c)
		}
	}
	return out
}

func (i endpointIndex) candidatesByHost(host string) []string {
	out := make([]string, 0)
	for _, variant := range canonicalHostVariants(host) {
		for _, c := range i.byHost[variant] {
			out = appendUnique(out, c)
		}
	}
	return out
}

func queryBool(db *sql.DB, q string) (bool, bool) {
	var v bool
	if err := db.QueryRow(q).Scan(&v); err != nil {
		return false, false
	}
	return v, true
}

func queryString(db *sql.DB, q string) (string, bool) {
	var v string
	if err := db.QueryRow(q).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func queryInt(db *sql.DB, q string) (int, bool) {
	var v int
	if err := db.QueryRow(q).Scan(&v); err != nil {
		return 0, false
	}
	return v, true
}

func collectPrimaryClients(db *sql.DB) []pgPrimaryClient {
	rows, err := db.Query(`
		SELECT
			COALESCE(client_hostname, ''),
			COALESCE(client_addr::text, ''),
			COALESCE(application_name, '')
		FROM pg_stat_replication
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]pgPrimaryClient, 0)
	for rows.Next() {
		var client pgPrimaryClient
		if err := rows.Scan(&client.Hostname, &client.Address, &client.ApplicationName); err != nil {
			continue
		}
		out = append(out, client)
	}
	return out
}

func collectLogicalSubscriptions(db *sql.DB) []pgLogicalSubscription {
	rows, err := db.Query(`
		SELECT
			s.subname,
			s.subconninfo,
			COALESCE(string_agg(DISTINCT sr.srrelid::regclass::text, ', '), '') AS tables_csv
		FROM pg_subscription s
		LEFT JOIN pg_subscription_rel sr ON sr.srsubid = s.oid
		GROUP BY s.subname, s.subconninfo
	`)
	if err == nil {
		defer rows.Close()
		out := make([]pgLogicalSubscription, 0)
		for rows.Next() {
			var sub pgLogicalSubscription
			if err := rows.Scan(&sub.Name, &sub.ConnInfo, &sub.TablesCSV); err != nil {
				continue
			}
			out = append(out, sub)
		}
		return out
	}

	rows, err = db.Query("SELECT subname, subconninfo FROM pg_subscription")
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]pgLogicalSubscription, 0)
	for rows.Next() {
		var sub pgLogicalSubscription
		if err := rows.Scan(&sub.Name, &sub.ConnInfo); err != nil {
			continue
		}
		out = append(out, sub)
	}
	return out
}

func mergeReplicationLinks(groups ...[]api.ReplicationInfo) []api.ReplicationInfo {
	out := make([]api.ReplicationInfo, 0)
	for _, g := range groups {
		out = append(out, g...)
	}
	return dedupeReplicationLinks(out)
}

func dedupeReplicationLinks(links []api.ReplicationInfo) []api.ReplicationInfo {
	seen := make(map[string]int, len(links))
	out := make([]api.ReplicationInfo, 0, len(links))

	for _, l := range links {
		l.SourceName = strings.TrimSpace(l.SourceName)
		l.TargetName = strings.TrimSpace(l.TargetName)
		l.Type = strings.TrimSpace(l.Type)
		l.Details = strings.TrimSpace(l.Details)
		if l.SourceName == "" || l.TargetName == "" || strings.EqualFold(l.SourceName, l.TargetName) {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(l.SourceName)) + "|" +
			strings.ToLower(strings.TrimSpace(l.TargetName)) + "|" +
			strings.ToLower(strings.TrimSpace(l.Type))

		if idx, ok := seen[key]; ok {
			if out[idx].Details == "" && l.Details != "" {
				out[idx].Details = l.Details
			}
			continue
		}

		seen[key] = len(out)
		out = append(out, l)
	}

	return out
}

func dedupeReplicationLinksWithDropped(links []api.ReplicationInfo) ([]api.ReplicationInfo, []api.ReplicationDroppedLink) {
	seen := make(map[string]int, len(links))
	out := make([]api.ReplicationInfo, 0, len(links))
	dropped := make([]api.ReplicationDroppedLink, 0)

	for _, l := range links {
		l.SourceName = strings.TrimSpace(l.SourceName)
		l.TargetName = strings.TrimSpace(l.TargetName)
		l.Type = strings.TrimSpace(l.Type)
		l.Details = strings.TrimSpace(l.Details)

		if l.SourceName == "" || l.TargetName == "" {
			dropped = append(dropped, api.ReplicationDroppedLink{
				SourceName: l.SourceName,
				TargetName: l.TargetName,
				Type:       l.Type,
				Reason:     "missing source or target",
			})
			continue
		}
		if strings.EqualFold(l.SourceName, l.TargetName) {
			dropped = append(dropped, api.ReplicationDroppedLink{
				SourceName: l.SourceName,
				TargetName: l.TargetName,
				Type:       l.Type,
				Reason:     "self-link",
			})
			continue
		}

		key := strings.ToLower(strings.TrimSpace(l.SourceName)) + "|" +
			strings.ToLower(strings.TrimSpace(l.TargetName)) + "|" +
			strings.ToLower(strings.TrimSpace(l.Type))

		if idx, ok := seen[key]; ok {
			if out[idx].Details == "" && l.Details != "" {
				out[idx].Details = l.Details
			}
			dropped = append(dropped, api.ReplicationDroppedLink{
				SourceName: l.SourceName,
				TargetName: l.TargetName,
				Type:       l.Type,
				Reason:     "duplicate",
			})
			continue
		}

		seen[key] = len(out)
		out = append(out, l)
	}

	return out, dropped
}

func parsePGConnInfo(s string) map[string]string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return map[string]string{}
	}

	// PostgreSQL conninfo can be URI-style (postgres:// or postgresql://).
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
		if uriFields := parsePGConnInfoURI(trimmed); len(uriFields) > 0 {
			return uriFields
		}
	}

	return parsePGConnInfoKV(trimmed)
}

func parsePGConnInfoKV(s string) map[string]string {
	out := make(map[string]string)
	for len(s) > 0 {
		s = strings.TrimLeft(s, " \t\n\r")
		if s == "" {
			break
		}

		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			break
		}

		key := strings.TrimSpace(s[:eq])
		s = s[eq+1:]
		if key == "" {
			break
		}

		val := ""
		if len(s) > 0 && s[0] == '\'' {
			s = s[1:]
			var b strings.Builder
			closed := false
			for i := 0; i < len(s); i++ {
				ch := s[i]
				if ch == '\\' && i+1 < len(s) {
					b.WriteByte(s[i+1])
					i++
					continue
				}
				if ch == '\'' {
					s = s[i+1:]
					closed = true
					break
				}
				b.WriteByte(ch)
			}
			if !closed {
				s = ""
			}
			val = b.String()
		} else {
			end := len(s)
			for i := 0; i < len(s); i++ {
				if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' {
					end = i
					break
				}
			}
			val = s[:end]
			s = s[end:]
		}

		out[strings.ToLower(key)] = strings.TrimSpace(val)
	}
	return out
}

func parsePGConnInfoURI(connURI string) map[string]string {
	u, err := url.Parse(connURI)
	if err != nil {
		return nil
	}

	out := make(map[string]string)

	if host := u.Hostname(); host != "" {
		out["host"] = host
	}
	if port := u.Port(); port != "" {
		out["port"] = port
	}

	dbname := strings.TrimPrefix(strings.TrimSpace(u.Path), "/")
	if dbname != "" {
		out["dbname"] = dbname
	}

	if u.User != nil {
		if user := u.User.Username(); user != "" {
			out["user"] = user
		}
		if pass, ok := u.User.Password(); ok {
			out["password"] = pass
		}
	}

	q := u.Query()
	if host := firstNonEmpty(q.Get("host"), q.Get("hostaddr")); host != "" {
		out["host"] = host
	}
	if port := q.Get("port"); port != "" {
		out["port"] = port
	}
	if db := q.Get("dbname"); db != "" {
		out["dbname"] = db
	}
	if user := q.Get("user"); user != "" {
		out["user"] = user
	}
	if pass := q.Get("password"); pass != "" {
		out["password"] = pass
	}

	return out
}

func parsePortDefault(s string, fallback int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	p, err := strconv.Atoi(s)
	if err != nil || p <= 0 {
		return fallback
	}
	return p
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.Trim(h, "[]")
	return h
}

func canonicalHostVariants(host string) []string {
	host = normalizeHost(host)
	if host == "" {
		return nil
	}

	variants := []string{host}
	switch host {
	case "localhost":
		variants = appendUnique(variants, "127.0.0.1")
		variants = appendUnique(variants, "::1")
	case "127.0.0.1":
		variants = appendUnique(variants, "localhost")
		variants = appendUnique(variants, "::1")
	case "::1":
		variants = appendUnique(variants, "localhost")
		variants = appendUnique(variants, "127.0.0.1")
	}

	// Allow matching short hostname against FQDN and vice-versa.
	if strings.Contains(host, ".") {
		short := strings.Split(host, ".")[0]
		variants = appendUnique(variants, short)
	}

	return variants
}

func normalizeDB(db string) string {
	return strings.ToLower(strings.TrimSpace(db))
}

func hostPortKey(host string, port int) string {
	if port <= 0 {
		port = 5432
	}
	return host + ":" + strconv.Itoa(port)
}

func hostPortDBKey(host string, port int, db string) string {
	return hostPortKey(host, port) + "/" + db
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func splitCSVTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func appendUnique(items []string, v string) []string {
	for _, item := range items {
		if item == v {
			return items
		}
	}
	return append(items, v)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
