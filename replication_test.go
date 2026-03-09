package main

import (
	"testing"

	"github.com/robotelu/db_barrel_2.0/internal/api"
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
	manual := []api.ReplicationInfo{
		{SourceName: "Primary", TargetName: "Replica", Type: "streaming"},
	}
	auto := []api.ReplicationInfo{
		{SourceName: "Primary", TargetName: "Replica", Type: "streaming"},
		{SourceName: "Publisher", TargetName: "Subscriber", Type: "logical"},
	}

	got := mergeReplicationLinks(manual, auto)
	if len(got) != 2 {
		t.Fatalf("expected 2 unique links, got %d: %#v", len(got), got)
	}
}
