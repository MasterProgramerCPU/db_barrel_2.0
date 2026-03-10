package main

import (
	"testing"

	"github.com/robotelu/db_barrel_2.0/internal/api"
	"github.com/robotelu/db_barrel_2.0/internal/config"
)

func TestParsePGConnInfo(t *testing.T) {
	connInfo := "host=10.0.0.11 port=5432 dbname=prod user=replicator password='s ec\\'ret' application_name=subscriber1"
	fields := parsePGConnInfo(connInfo)

	if fields["host"] != "10.0.0.11" {
		t.Fatalf("expected host, got %q", fields["host"])
	}
	if fields["port"] != "5432" {
		t.Fatalf("expected port, got %q", fields["port"])
	}
	if fields["dbname"] != "prod" {
		t.Fatalf("expected dbname, got %q", fields["dbname"])
	}
	if fields["password"] != "s ec'ret" {
		t.Fatalf("expected unescaped quoted password, got %q", fields["password"])
	}
}

func TestParsePGConnInfoURI(t *testing.T) {
	connInfo := "postgresql://replicator:s%20ec%27ret@primary.db.local:5432/prod?application_name=subscriber1"
	fields := parsePGConnInfo(connInfo)

	if fields["host"] != "primary.db.local" {
		t.Fatalf("expected host, got %q", fields["host"])
	}
	if fields["port"] != "5432" {
		t.Fatalf("expected port, got %q", fields["port"])
	}
	if fields["dbname"] != "prod" {
		t.Fatalf("expected dbname, got %q", fields["dbname"])
	}
	if fields["password"] != "s ec'ret" {
		t.Fatalf("expected decoded password, got %q", fields["password"])
	}
}

func TestEndpointIndexFind(t *testing.T) {
	idx := buildEndpointIndex([]pgEndpoint{
		{Name: "PrimaryA", Host: "pg-a.local", Port: 5432, Database: "app"},
		{Name: "ReplicaA", Host: "pg-a-replica.local", Port: 5432, Database: "app"},
		{Name: "PrimaryB", Host: "10.0.0.2", Port: 5432, Database: "metrics"},
	})

	if name, ok := idx.find("pg-a.local", 5432, "app"); !ok || name != "PrimaryA" {
		t.Fatalf("expected PrimaryA exact match, got ok=%v name=%q", ok, name)
	}

	// Fallback to unique host:port match when dbname is missing.
	if name, ok := idx.find("10.0.0.2", 5432, ""); !ok || name != "PrimaryB" {
		t.Fatalf("expected PrimaryB host:port match, got ok=%v name=%q", ok, name)
	}

	// Unknown host should not match.
	if _, ok := idx.find("unknown", 5432, "app"); ok {
		t.Fatal("expected no match for unknown host")
	}
}

func TestMergeReplicationLinksDedupes(t *testing.T) {
	auto := []api.ReplicationInfo{
		{SourceName: "Primary", TargetName: "Replica", Type: "streaming"},
		{SourceName: "Publisher", TargetName: "Subscriber", Type: "logical", Details: "tables: public.users"},
	}
	manual := []api.ReplicationInfo{
		{SourceName: "Primary", TargetName: "Replica", Type: "streaming"},
		{SourceName: "Publisher", TargetName: "Subscriber", Type: "logical"},
	}

	got := mergeReplicationLinks(auto, manual)
	if len(got) != 2 {
		t.Fatalf("expected 2 unique links, got %d: %#v", len(got), got)
	}
	if got[1].Details == "" {
		t.Fatalf("expected logical details to be preserved, got %#v", got[1])
	}
}

func TestMatchSourceEndpointFromHostList(t *testing.T) {
	idx := buildEndpointIndex([]pgEndpoint{
		{Name: "Primary", Host: "primary.db.local", Port: 5432, Database: "prod"},
		{Name: "Replica", Host: "replica.db.local", Port: 5432, Database: "prod"},
	})

	fields := parsePGConnInfo("host=primary.db.local,backup.db.local port=5432,5432 dbname=prod")
	source, ok := matchSourceEndpoint(fields, idx)
	if !ok || source != "Primary" {
		t.Fatalf("expected source Primary, got ok=%v source=%q", ok, source)
	}
}

func TestMatchSourceEndpointLocalhostVariants(t *testing.T) {
	idx := buildEndpointIndex([]pgEndpoint{
		{Name: "Primary", Host: "localhost", Port: 5432, Database: "prod"},
	})

	fields := parsePGConnInfo("host=127.0.0.1 port=5432 dbname=prod")
	source, ok := matchSourceEndpoint(fields, idx)
	if !ok || source != "Primary" {
		t.Fatalf("expected localhost variant to match Primary, got ok=%v source=%q", ok, source)
	}
}

func TestEndpointFindPrefersExactDBOverSharedHostPort(t *testing.T) {
	idx := buildEndpointIndex([]pgEndpoint{
		{Name: "Primary", Host: "pg.local", Port: 5432, Database: "prod"},
		{Name: "Subscriber", Host: "pg.local", Port: 5432, Database: "analytics"},
	})

	name, ok := idx.find("pg.local", 5432, "prod")
	if !ok || name != "Primary" {
		t.Fatalf("expected exact db match Primary, got ok=%v name=%q", ok, name)
	}
}

func TestBuildObservedEndpointIndexMatchesServerAddressAlias(t *testing.T) {
	idx := buildObservedEndpointIndex([]pgEndpointObservation{
		{
			Endpoint:         pgEndpoint{Name: "Primary", Host: "postgres-primary", Port: 5432, Database: "prod"},
			ServerAddress:    "10.20.30.40",
			ServerPort:       5432,
			SystemIdentifier: "sys-a",
		},
	})

	source, ok := matchSourceEndpoint(parsePGConnInfo("host=10.20.30.40 port=5432 dbname=prod"), idx)
	if !ok || source != "Primary" {
		t.Fatalf("expected server address alias to match Primary, got ok=%v source=%q", ok, source)
	}
}

func TestResolveStandbySourceFallsBackToSystemIdentifier(t *testing.T) {
	idx := buildObservedEndpointIndex([]pgEndpointObservation{
		{
			Endpoint:         pgEndpoint{Name: "Primary", Host: "primary.internal", Port: 5432, Database: "prod"},
			ServerAddress:    "10.0.0.1",
			ServerPort:       5432,
			SystemIdentifier: "sys-123",
		},
		{
			Endpoint:         pgEndpoint{Name: "Replica", Host: "replica.internal", Port: 5432, Database: "prod"},
			ServerAddress:    "10.0.0.2",
			ServerPort:       5432,
			SystemIdentifier: "sys-123",
		},
	})

	resolution, ok := resolveStandbySource(
		pgEndpointObservation{
			Endpoint:         pgEndpoint{Name: "Replica", Host: "replica.internal", Port: 5432, Database: "prod"},
			SystemIdentifier: "sys-123",
			StandbyConnFields: []map[string]string{
				parsePGConnInfo("host=unmatched.internal port=5432 dbname=prod"),
			},
		},
		idx,
		map[string]bool{"Primary": true},
		[]string{"Primary"},
		map[string][]string{"sys-123": {"Primary"}},
	)
	if !ok || resolution.SourceName != "Primary" {
		t.Fatalf("expected system identifier fallback to match Primary, got ok=%v resolution=%+v", ok, resolution)
	}
}

func TestManualReplicationLinksDefaultsType(t *testing.T) {
	cfg := &config.Config{
		Databases: []config.DatabaseConfig{
			{Name: "Primary", Driver: "sqlite", Path: ":memory:"},
		},
		Replication: []config.ReplicationConfig{
			{SourceName: "Primary", TargetName: "Replica"},
			{SourceName: "Publisher", TargetName: "Subscriber", Type: "logical", Details: "users"},
		},
	}

	got := manualReplicationLinks(cfg)
	if len(got) != 2 {
		t.Fatalf("expected 2 manual links, got %d", len(got))
	}
	if got[0].Type != "streaming" {
		t.Fatalf("expected default type 'streaming', got %q", got[0].Type)
	}
	if got[1].Type != "logical" || got[1].Details != "users" {
		t.Fatalf("expected logical link details to be preserved, got %#v", got[1])
	}
}
