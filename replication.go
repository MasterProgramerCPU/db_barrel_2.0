package main

import (
	"database/sql"
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
	byHostPortDB map[string]string
	byHostPort   map[string][]string
	byHost       map[string][]string
}

func buildReplication(cfg *config.Config) []api.ReplicationInfo {
	manual := replicationFromConfig(cfg)
	auto := discoverPostgresReplication(cfg)
	return mergeReplicationLinks(manual, auto)
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

		// Streaming replication (standby -> primary mapping via primary_conninfo).
		if inRecovery, ok := queryBool(db, "SELECT pg_is_in_recovery()"); ok && inRecovery {
			if primaryConnInfo, ok := queryString(db, "SELECT COALESCE(current_setting('primary_conninfo', true), '')"); ok {
				fields := parsePGConnInfo(primaryConnInfo)
				sourceHost := firstNonEmpty(fields["host"], fields["hostaddr"])
				sourcePort := parsePortDefault(fields["port"], 5432)
				if sourceName, ok := idx.find(sourceHost, sourcePort, ""); ok && sourceName != ep.Name {
					links = append(links, api.ReplicationInfo{
						SourceName: sourceName,
						TargetName: ep.Name,
						Type:       "streaming",
					})
				}
			}
		} else if ok {
			// Best effort fallback if we can only inspect primaries.
			rows, err := db.Query("SELECT client_addr::text FROM pg_stat_replication WHERE client_addr IS NOT NULL")
			if err == nil {
				for rows.Next() {
					var clientAddr string
					if err := rows.Scan(&clientAddr); err != nil {
						continue
					}
					if targetName, ok := idx.findByHost(clientAddr); ok && targetName != ep.Name {
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

		// Logical replication (subscription defines source conninfo).
		rows, err := db.Query("SELECT subconninfo FROM pg_subscription")
		if err == nil {
			for rows.Next() {
				var connInfo string
				if err := rows.Scan(&connInfo); err != nil {
					continue
				}
				fields := parsePGConnInfo(connInfo)
				sourceHost := firstNonEmpty(fields["host"], fields["hostaddr"])
				sourcePort := parsePortDefault(fields["port"], 5432)
				sourceDB := fields["dbname"]
				if sourceName, ok := idx.find(sourceHost, sourcePort, sourceDB); ok && sourceName != ep.Name {
					links = append(links, api.ReplicationInfo{
						SourceName: sourceName,
						TargetName: ep.Name,
						Type:       "logical",
					})
				}
			}
			rows.Close()
		}

		db.Close()
	}

	return dedupeReplicationLinks(links)
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
		byHostPortDB: make(map[string]string, len(endpoints)),
		byHostPort:   make(map[string][]string, len(endpoints)),
		byHost:       make(map[string][]string, len(endpoints)),
	}
	for _, ep := range endpoints {
		host := normalizeHost(ep.Host)
		hp := hostPortKey(host, ep.Port)
		hpd := hostPortDBKey(host, ep.Port, normalizeDB(ep.Database))
		idx.byHostPortDB[hpd] = ep.Name
		idx.byHostPort[hp] = appendUnique(idx.byHostPort[hp], ep.Name)
		idx.byHost[host] = appendUnique(idx.byHost[host], ep.Name)
	}
	return idx
}

func (i endpointIndex) find(host string, port int, database string) (string, bool) {
	host = normalizeHost(host)
	database = normalizeDB(database)
	if host == "" {
		return "", false
	}

	if database != "" {
		if name, ok := i.byHostPortDB[hostPortDBKey(host, port, database)]; ok {
			return name, true
		}
	}

	candidates := i.byHostPort[hostPortKey(host, port)]
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return "", false
}

func (i endpointIndex) findByHost(host string) (string, bool) {
	candidates := i.byHost[normalizeHost(host)]
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return "", false
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
	seen := make(map[string]struct{}, len(links))
	out := make([]api.ReplicationInfo, 0, len(links))
	for _, l := range links {
		if l.SourceName == "" || l.TargetName == "" || l.SourceName == l.TargetName {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(l.SourceName)) + "|" +
			strings.ToLower(strings.TrimSpace(l.TargetName)) + "|" +
			strings.ToLower(strings.TrimSpace(l.Type))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
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

func appendUnique(items []string, v string) []string {
	for _, item := range items {
		if item == v {
			return items
		}
	}
	return append(items, v)
}
