package store_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/casprine/inbox/server/internal/migrate"
	"github.com/casprine/inbox/server/internal/store"
)

// newStore migrates into a throwaway schema and drops it on cleanup.
func newStore(t *testing.T) *store.Store {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	url := fmt.Sprintf("%s%ssearch_path=%s,public", base, sep, schema)
	if err := migrate.Up(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return store.New(pool)
}

func newBiz(t *testing.T, s *store.Store, domain string, order int) store.Business {
	t.Helper()
	b, err := s.CreateBusiness(context.Background(), store.Business{ID: uuid.Must(uuid.NewV7()), Name: domain, Domain: domain, CFZoneID: "z", SortOrder: order, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newMsg(biz *uuid.UUID, hdr string, at time.Time) store.Message {
	return store.Message{ID: uuid.Must(uuid.NewV7()), BusinessID: biz, Source: "edge", MessageIDHdr: hdr, ThreadKey: hdr, ReceivedAt: at}
}

func TestBusinessesUnreadAndOrder(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a, b := newBiz(t, s, "a.com", 0), newBiz(t, s, "b.com", 1)
	now := time.Now().UTC().Truncate(time.Microsecond)
	var ids []uuid.UUID
	for i := range 3 {
		m := newMsg(&a.ID, fmt.Sprintf("<%d@x>", i), now.Add(time.Duration(i)*time.Second))
		if ok, err := s.InsertMessage(ctx, m); err != nil || !ok {
			t.Fatal(ok, err)
		}
		ids = append(ids, m.ID)
	}
	s.SetMessagesRead(ctx, ids[:1], true)
	s.SetMessagesArchived(ctx, ids[1:2], true)
	list, err := s.ListBusinessesWithUnread(ctx)
	if err != nil || len(list) != 2 || list[0].Domain != "a.com" || list[0].Unread != 1 || list[1].Unread != 0 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if err := s.SetBusinessOrder(ctx, []uuid.UUID{b.ID, a.ID}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListBusinessesWithUnread(ctx)
	if list[0].ID != b.ID {
		t.Fatalf("order not applied: %+v", list)
	}
	name, routing := "B2", "ok"
	u, err := s.UpdateBusiness(ctx, b.ID, store.BusinessPatch{Name: &name, Routing: &routing})
	if err != nil || u.Name != "B2" || u.Routing != "ok" || u.RoutingCheckedAt == nil || !u.Enabled {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := s.GetBusiness(ctx, uuid.New()); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestAddressesUpsert(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := newBiz(t, s, "a.com", 0)
	t1 := time.Now().UTC().Truncate(time.Microsecond)
	s.UpsertBusinessAddress(ctx, b.ID, "invoices", t1)
	s.UpsertBusinessAddress(ctx, b.ID, "invoices", t1.Add(-time.Hour))
	s.UpsertBusinessAddress(ctx, b.ID, "hello", t1)
	as, err := s.ListBusinessAddresses(ctx, b.ID)
	if err != nil || len(as) != 2 || as[0].LocalPart != "invoices" || as[0].MsgCount != 2 || !as[0].LastAt.Equal(t1) {
		t.Fatalf("%+v %v", as, err)
	}
}

func TestMessagesInsertConflictListAndFlags(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := newBiz(t, s, "a.com", 0)
	base := time.Now().UTC().Truncate(time.Microsecond)
	m0 := newMsg(&b.ID, "<dup@x>", base)
	if ok, err := s.InsertMessage(ctx, m0); !ok || err != nil {
		t.Fatal(ok, err)
	}
	dup := newMsg(&b.ID, "<dup@x>", base)
	if ok, err := s.InsertMessage(ctx, dup); ok || err != nil {
		t.Fatalf("duplicate should be no-op: %v %v", ok, err)
	}
	for i := 1; i < 5; i++ {
		m := newMsg(&b.ID, fmt.Sprintf("<%d@x>", i), base.Add(time.Duration(i)*time.Minute))
		m.AuthResults = []byte(`{"spf":"pass"}`)
		if ok, err := s.InsertMessage(ctx, m); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	// keyset paging, 2 per page, newest first, no overlap
	var seen []string
	var cur *store.Cursor
	for {
		page, err := s.ListMessages(ctx, store.ListMessagesParams{BusinessID: &b.ID, Limit: 2, Cursor: cur})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page {
			seen = append(seen, m.MessageIDHdr)
		}
		if len(page) < 2 {
			break
		}
		last := page[len(page)-1]
		cur = &store.Cursor{ReceivedAt: last.ReceivedAt, ID: last.ID}
	}
	want := []string{"<4@x>", "<3@x>", "<2@x>", "<1@x>", "<dup@x>"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v", seen)
	}
	got, err := s.GetMessage(ctx, m0.ID)
	if err != nil || got.Seq == 0 || got.Route != "pending" || got.State != "stored" || got.IsRead || len(got.EnvToAll) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	if n, _ := s.SetMessagesRead(ctx, []uuid.UUID{m0.ID}, true); n != 1 {
		t.Fatal("read not applied")
	}
	if n, _ := s.SetMessagesRead(ctx, []uuid.UUID{m0.ID}, true); n != 0 {
		t.Fatal("read should be idempotent")
	}
	unread, _ := s.ListMessages(ctx, store.ListMessagesParams{UnreadOnly: true})
	if len(unread) != 4 {
		t.Fatalf("unread=%d", len(unread))
	}
	s.SetMessagesArchived(ctx, []uuid.UUID{m0.ID}, true)
	all, _ := s.ListMessages(ctx, store.ListMessagesParams{})
	if len(all) != 4 {
		t.Fatalf("archived should be hidden, got %d", len(all))
	}
	g, _ := s.GetMessage(ctx, m0.ID)
	if g.ArchivedAt == nil || g.ReadAt == nil {
		t.Fatal("timestamps not set")
	}
	if _, err := s.GetMessageByHeaderID(ctx, "<nope>"); err != store.ErrNotFound {
		t.Fatal(err)
	}
}

func TestAttachmentsSearchLogSettingsTx(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := newBiz(t, s, "a.com", 0)
	m := newMsg(&b.ID, "<s@x>", time.Now().UTC())
	fn := "invoice.pdf"
	err := s.WithTx(ctx, func(q *store.Queries) error {
		if _, err := q.InsertMessage(ctx, m); err != nil {
			return err
		}
		if err := q.InsertAttachment(ctx, store.Attachment{ID: uuid.Must(uuid.NewV7()), MessageID: m.ID, Filename: &fn, R2Key: "att/1"}); err != nil {
			return err
		}
		if err := q.UpsertMessageSearch(ctx, m.ID, "Northgate invoice", "billing@northgate.com", "please pay", fn); err != nil {
			return err
		}
		return q.AppendIngestLog(ctx, "raw/1.eml", &m.ID, "info", "stored")
	})
	if err != nil {
		t.Fatal(err)
	}
	as, _ := s.ListAttachments(ctx, m.ID)
	if len(as) != 1 || *as[0].Filename != fn {
		t.Fatalf("%+v", as)
	}
	ids, err := s.SearchMessageIDs(ctx, "northgate", 10)
	if err != nil || len(ids) != 1 || ids[0] != m.ID {
		t.Fatalf("%v %v", ids, err)
	}
	if lines, _ := s.ListIngestLog(ctx, "raw/1.eml"); len(lines) != 1 || lines[0].Line != "stored" {
		t.Fatalf("%+v", lines)
	}
	// rollback leaves nothing behind
	rb := newMsg(&b.ID, "<rb@x>", time.Now().UTC())
	_ = s.WithTx(ctx, func(q *store.Queries) error {
		q.InsertMessage(ctx, rb)
		return fmt.Errorf("boom")
	})
	if _, err := s.GetMessage(ctx, rb.ID); err != store.ErrNotFound {
		t.Fatalf("rollback failed: %v", err)
	}
	// cascade: deleting the message drops attachments/search
	if _, err := s.Pool().Exec(ctx, `DELETE FROM messages WHERE id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.SearchMessageIDs(ctx, "northgate", 10); len(ids) != 0 {
		t.Fatal("search row not cascaded")
	}
	if _, err := s.GetSetting(ctx, "retention_days"); err != store.ErrNotFound {
		t.Fatal(err)
	}
	s.SetSetting(ctx, "retention_days", 90)
	s.SetSetting(ctx, "retention_days", 30)
	v, _ := s.GetSetting(ctx, "retention_days")
	if string(v) != "30" {
		t.Fatalf("%s", v)
	}
}
