package backfill_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/rules"
)

func newRule(biz uuid.UUID, local string, label *string) rules.Rule {
	return rules.Rule{ID: ids.NewV7(), Name: "Invoices", Enabled: true, BusinessID: &biz, MatchToLocal: &local,
		GmailLabel: label, GmailArchive: true, GmailMarkRead: true, LabelOnly: false}
}

func mustParse(t *testing.T, raw []byte) *parse.Parsed {
	t.Helper()
	p, err := parse.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
