package store

import (
	"context"
	"testing"
)

func TestUsageReceiptsCommitWithMetadataAndSurviveDeletion(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	input := UpsertPreview{Slug: "private-title", PublicURL: "https://private.invalid/content", Title: "private-content", FileCount: 1}
	if _, err := d.Upsert(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Upsert(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetPassword(ctx, input.Slug, "private-hash"); err != nil {
		t.Fatal(err)
	}
	if removed, err := d.Delete(ctx, input.Slug); err != nil || !removed {
		t.Fatalf("delete %v %v", removed, err)
	}
	if removed, err := d.Delete(ctx, input.Slug); err != nil || removed {
		t.Fatalf("missing delete %v %v", removed, err)
	}
	var publishes, deletes int
	if err := d.db.QueryRow(`SELECT count(*) FROM usage_outbox WHERE action='publish'`).Scan(&publishes); err != nil {
		t.Fatal(err)
	}
	if err := d.db.QueryRow(`SELECT count(*) FROM usage_outbox WHERE action='delete'`).Scan(&deletes); err != nil {
		t.Fatal(err)
	}
	if publishes != 2 || deletes != 1 {
		t.Fatalf("receipts publish=%d delete=%d", publishes, deletes)
	}
	rows, err := d.db.Query(`PRAGMA table_info(usage_outbox)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name != "seq" && name != "event_id" && name != "occurred_at" && name != "action" {
			t.Fatalf("unexpected retained field %s", name)
		}
	}
}

func TestFailedReceiptRollsBackMetadata(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.db.Exec(`CREATE TRIGGER reject_receipt BEFORE INSERT ON usage_outbox BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := d.Upsert(ctx, UpsertPreview{Slug: "rollback", PublicURL: "https://fixture.invalid"}); err == nil {
		t.Fatal("expected receipt failure")
	}
	if _, found, err := d.Get(ctx, "rollback"); err != nil || found {
		t.Fatalf("metadata survived failed transaction %v %v", found, err)
	}
}
