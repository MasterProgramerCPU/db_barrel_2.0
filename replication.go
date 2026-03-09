package main

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"

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
}

func buildReplication(cfg *config.Config) []api.ReplicationInfo {
	auto := discoverPostgresReplication(cfg)
	manual := replicationFromConfig(cfg)
	return mergeReplicationLinks(auto, manual)
}

func replicationFromConfig(cfg *config.Config) []api.ReplicationInfo {
	repl := make([]api.ReplicationInfo, 0, len(cfg.Replication))
	for _, r := range cfg.Replication {
		repl = append(repl, api.ReplicationInfo{
			SourceName: r.SourceName,
			TargetName: r.TargetName,
			Type:       r.Type,
		})
	}
	return repl
}

func discoverPostgresReplication(cfg *config.Config) []api.ReplicationInfo {
	endpoints := postgresEndpoints(cfg)
	if len(endpoints) == 0 {
		return nil
	}

	idx := buildEndpointIndex(endpoints)
	links := make([]api.ReplicationInfo, 0)
	primarySet := make(map[string]bool, len(endpoints))
	unmatchedStandbys := make(map[string][]map[string]string)

	for _, ep := range endpoints {
		db, err := sql.Open("postgres", ep.DSN)
		if err != nil {
			log.Printf("  ⚠️ [%s] replication discover: open failed: %v", ep.Name, err)
			continue
		}

		if err := db.Ping(); err != nil {
			log.Printf("  ⚠️ [%s] replication discover: ping failed: %v", ep.Name, err)
			db.Close()
			continue
		}

		if inRecovery, ok := queryBool(db, "SELECT pg_is_in_recovery()"); ok && inRecovery {
			if sourceName, ok, connFields := discoverStreamingSourceFromStandby(db, idx); ok && sourceName != ep.Name {
				links = append(links, api.ReplicationInfo{
					SourceName: sourceName,
					TargetName: ep.Name,
					Type:       "streaming",
				})
			} else if len(connFields) > 0 {
				unmatchedStandbys[ep.Name] = connFields
			}
		} else if ok {
			primarySet[ep.Name] = true

			// Best effort fallback when we can query only the primary side.
			rows, err := db.Query(`
				SELECT COALESCE(client_hostname, ''), COALESCE(client_addr::text, '')
				FROM pg_stat_replication
			`)
			if err == nil {
				for rows.Next() {
					var clientHost, clientAddr string
					if err := rows.Scan(&clientHost, &clientAddr); err != nil {
						continue
					}
					host := firstNonEmpty(clientHost, clientAddr)
					if targetName, ok := idx.findByHost(host); ok && targetName != ep.Name {
						links = append(links, api.ReplicationInfo{
							SourceName: ep.Name,
							TargetName: targetName,
							Type:       "streaming",
						})
					}
				}
				rows.Close()
			}
		}

		// Logical replication (subscription defines source conninfo + replicated tables on subscriber).
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
			for rows.Next() {
				var subName, connInfo, tablesCSV string
				if err := rows.Scan(&subName, &connInfo, &tablesCSV); err != nil {
					continue
				}

				fields := parsePGConnInfo(connInfo)
				sourceName, ok := matchSourceEndpoint(fields, idx)
				if !ok || sourceName == ep.Name {
					continue
				}

				links = append(links, api.ReplicationInfo{
					SourceName: sourceName,
					TargetName: ep.Name,
					Type:       "logical",
					Details:    logicalDetails(subName, tablesCSV),
				})
			}
			rows.Close()
		} else {
			// Fallback when pg_subscription_rel join is not accessible.
			rows, err = db.Query("SELECT subname, subconninfo FROM pg_subscription")
			if err == nil {
				for rows.Next() {
					var subName, connInfo string
					if err := rows.Scan(&subName, &connInfo); err != nil {
						continue
					}
					fields := parsePGConnInfo(connInfo)
					sourceName, ok := matchSourceEndpoint(fields, idx)
					if !ok || sourceName == ep.Name {
						continue
					}
					links = append(links, api.ReplicationInfo{
						SourceName: sourceName,
						TargetName: ep.Name,
						Type:       "logical",
						Details:    logicalDetails(subName, ""),
					})
				}
				rows.Close()
			}
		}

		db.Close()
	}

	primaryNames := mapKeys(primarySet)
	for standbyName, connFields := range unmatchedStandbys {
		if sourceName, ok := inferStreamingSource(connFields, idx, primarySet, primaryNames); ok && sourceName != standbyName {
			links = append(links, api.ReplicationInfo{
				SourceName: sourceName,
				TargetName: standbyName,
				Type:       "streaming",
			})
		}
	}

	return dedupeReplicationLinks(links)
}

func discoverStreamingSourceFromStandby(db *sql.DB, idx endpointIndex) (string, bool, []map[string]string) {
	connInfoCandidates := make([]string, 0, 2)

	// primary_conninfo is often available on standbys.
	if primaryConnInfo, ok := queryString(db, "SELECT COALESCE(current_setting('primary_conninfo', true), '')"); ok && strings.TrimSpace(primaryConnInfo) != "" {
		connInfoCandidates = append(connInfoCandidates, primaryConnInfo)
	}

	// Fallback for setups where current_setting is unavailable/restricted.
	if walConnInfo, ok := queryString(db, "SELECT COALESCE(conninfo, '') FROM pg_stat_wal_receiver LIMIT 1"); ok && strings.TrimSpace(walConnInfo) != "" {
		connInfoCandidates = append(connInfoCandidates, walConnInfo)
	}

	connFields := make([]map[string]string, 0, len(connInfoCandidates))
	for _, connInfo := range connInfoCandidates {
		fields := parsePGConnInfo(connInfo)
		connFields = append(connFields, fields)
		if sourceName, ok := matchSourceEndpoint(fields, idx); ok {
			return sourceName, true, connFields
		}
	}

	return "", false, connFields
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

	if subName == "" {
		return "tables: " + strings.Join(visible, ", ") + suffix
	}
	return "tables: " + strings.Join(visible, ", ") + suffix
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
	}
	for _, ep := range endpoints {
		dbKey := normalizeDB(ep.Database)
		if dbKey != "" {
			idx.byDB[dbKey] = appendUnique(idx.byDB[dbKey], ep.Name)
		}
		for _, host := range canonicalHostVariants(ep.Host) {
			hp := hostPortKey(host, ep.Port)
			hpd := hostPortDBKey(host, ep.Port, dbKey)
			idx.byHostPortDB[hpd] = appendUnique(idx.byHostPortDB[hpd], ep.Name)
			idx.byHostPort[hp] = appendUnique(idx.byHostPort[hp], ep.Name)
			idx.byHost[host] = appendUnique(idx.byHost[host], ep.Name)
		}
	}
	return idx
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
		if l.SourceName == "" || l.TargetName == "" || l.SourceName == l.TargetName {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(l.SourceName)) + "|" +
			strings.ToLower(strings.TrimSpace(l.TargetName)) + "|" +
			strings.ToLower(strings.TrimSpace(l.Type))

		if idx, ok := seen[key]; ok {
			if out[idx].Details == "" && strings.TrimSpace(l.Details) != "" {
				out[idx].Details = strings.TrimSpace(l.Details)
			}
			continue
		}

		l.Details = strings.TrimSpace(l.Details)
		seen[key] = len(out)
		out = append(out, l)
	}

	return out
}

func parsePGConnInfo(s string) map[string]string {
	out := make(map[string]string)
	s = strings.TrimSpace(s)
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
