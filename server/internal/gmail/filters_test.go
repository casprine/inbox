package gmail

import (
	"testing"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/store"
)

func rawFilter(from, to, subject string, add, remove []string) RawFilter {
	var f RawFilter
	f.ID = "f1"
	f.Criteria.From, f.Criteria.To, f.Criteria.Subject = from, to, subject
	f.Action.AddLabelIDs, f.Action.RemoveLabelIDs = add, remove
	return f
}

func TestDescribeFilter(t *testing.T) {
	acme := store.Business{ID: uuid.New(), Domain: "acme.com"}
	biz := []store.Business{acme}
	names := map[string]string{"L1": "Acme/Invoices", "L2": "Aaa", "L3": "Zed"}

	cases := []struct {
		name       string
		f          RawFilter
		importable bool
		reason     string
		check      func(t *testing.T, f FilterInfo)
	}{
		{"from domain with label", rawFilter("@stripe.com", "", "", []string{"L1"}, nil), true, "", func(t *testing.T, f FilterInfo) {
			if *f.rule.MatchFrom != "@stripe.com" || *f.rule.GmailLabel != "Acme/Invoices" || !f.rule.LabelOnly || f.rule.BusinessID != nil {
				t.Fatalf("%+v", f.rule)
			}
			if f.rule.Name != "Invoices" || *f.rule.ImportedFromFilter != "f1" {
				t.Fatalf("name/imported: %+v", f.rule)
			}
		}},
		{"archive and read flags reported but rule stays label only", rawFilter("a@b.com", "", "", []string{"L1"}, []string{"INBOX", "UNREAD"}), true, "", func(t *testing.T, f FilterInfo) {
			if !f.Archive || !f.MarkRead || !f.rule.LabelOnly || f.rule.GmailArchive || f.rule.GmailMarkRead {
				t.Fatalf("%+v %+v", f, f.rule)
			}
		}},
		{"to managed address", rawFilter("", "Invoices@Acme.com", "", []string{"L1"}, nil), true, "", func(t *testing.T, f FilterInfo) {
			if *f.rule.BusinessID != acme.ID || *f.rule.MatchToLocal != "invoices" {
				t.Fatalf("%+v", f.rule)
			}
		}},
		{"to whole domain", rawFilter("", "*@acme.com", "", []string{"L1"}, nil), true, "", func(t *testing.T, f FilterInfo) {
			if *f.rule.BusinessID != acme.ID || f.rule.MatchToLocal != nil {
				t.Fatalf("%+v", f.rule)
			}
		}},
		{"bare domain from", rawFilter("stripe.com", "", "", []string{"L1"}, nil), true, "", func(t *testing.T, f FilterInfo) {
			if *f.rule.MatchFrom != "@stripe.com" {
				t.Fatal(*f.rule.MatchFrom)
			}
		}},
		{"subject is escaped literally", rawFilter("", "", `"Invoice #4.5+"`, []string{"L1"}, nil), true, "", func(t *testing.T, f FilterInfo) {
			if *f.rule.MatchSubject != `Invoice #4\.5\+` {
				t.Fatal(*f.rule.MatchSubject)
			}
		}},
		{"several labels: first by name", rawFilter("a@b.com", "", "", []string{"L3", "L2"}, nil), true, "", func(t *testing.T, f FilterInfo) {
			if *f.Label != "Aaa" {
				t.Fatal(*f.Label)
			}
		}},
		{"system labels are not custom labels", rawFilter("a@b.com", "", "", []string{"STARRED", "IMPORTANT"}, nil), false, ReasonNoLabel, nil},
		{"delete via trash", rawFilter("a@b.com", "", "", []string{"TRASH"}, nil), false, ReasonDeletesMail, nil},
		{"delete wins over label", rawFilter("a@b.com", "", "", []string{"L1", "TRASH"}, nil), false, ReasonDeletesMail, nil},
		{"spam counts as delete", rawFilter("a@b.com", "", "", []string{"SPAM"}, nil), false, ReasonDeletesMail, nil},
		{"forward", func() RawFilter {
			f := rawFilter("a@b.com", "", "", []string{"L1"}, nil)
			f.Action.Forward = "x@y.com"
			return f
		}(), false, ReasonForwardsMail, nil},
		{"query criteria", func() RawFilter {
			f := rawFilter("a@b.com", "", "", []string{"L1"}, nil)
			f.Criteria.Query = "has:attachment"
			return f
		}(), false, ReasonUnsupportedCriteria, nil},
		{"size criteria", func() RawFilter {
			f := rawFilter("a@b.com", "", "", []string{"L1"}, nil)
			f.Criteria.Size = 5
			return f
		}(), false, ReasonUnsupportedCriteria, nil},
		{"no conditions", rawFilter("", "", "", []string{"L1"}, nil), false, ReasonUnsupportedCriteria, nil},
		{"from with OR", rawFilter("a@b.com OR c@d.com", "", "", []string{"L1"}, nil), false, ReasonUnsupportedCriteria, nil},
		{"from bare word", rawFilter("stripe", "", "", []string{"L1"}, nil), false, ReasonUnsupportedCriteria, nil},
		{"to without domain", rawFilter("", "invoices", "", []string{"L1"}, nil), false, ReasonUnsupportedCriteria, nil},
		{"to other domain", rawFilter("", "x@other.com", "", []string{"L1"}, nil), false, ReasonUnmanagedDomain, nil},
		{"subject with grouping", rawFilter("", "", "Receipt (2024)", []string{"L1"}, nil), false, ReasonUnsupportedCriteria, nil},
		{"subject negation", rawFilter("", "", "invoice -draft", []string{"L1"}, nil), false, ReasonUnsupportedCriteria, nil},
		{"unknown label id ignored", rawFilter("a@b.com", "", "", []string{"Label_gone"}, nil), false, ReasonNoLabel, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := DescribeFilter(tc.f, names, biz)
			if f.Importable != tc.importable {
				t.Fatalf("importable=%v reason=%v", f.Importable, f.Reason)
			}
			if tc.importable {
				if f.Reason != nil || f.Rule == nil {
					t.Fatalf("importable filter must have a rule preview and no reason: %+v", f)
				}
			} else if f.Reason == nil || *f.Reason != tc.reason || f.Rule != nil {
				got := "<nil>"
				if f.Reason != nil {
					got = *f.Reason
				}
				t.Fatalf("reason %s, want %s", got, tc.reason)
			}
			if tc.check != nil {
				tc.check(t, f)
			}
		})
	}
}
