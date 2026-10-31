package gmail

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/casprine/inbox/server/internal/rules"
	"github.com/casprine/inbox/server/internal/store"
)

// Reasons a Gmail filter cannot become a rule.
const (
	ReasonDeletesMail         = "deletes_mail"
	ReasonForwardsMail        = "forwards_mail"
	ReasonNoLabel             = "no_label"
	ReasonUnsupportedCriteria = "unsupported_criteria"
	ReasonUnmanagedDomain     = "unmanaged_domain"
)

// FilterInfo is a Gmail filter described for the import screen.
type FilterInfo struct {
	ID       string  `json:"id"`
	From     *string `json:"from"`
	To       *string `json:"to"`
	Subject  *string `json:"subject"`
	Label    *string `json:"label"`
	Archive  bool    `json:"archive"`
	MarkRead bool    `json:"mark_read"`
	Delete   bool    `json:"delete"`
	Forward  *string `json:"forward"`
	// Importable is true when the filter maps to a label-only rule; Reason says why not otherwise.
	Importable      bool    `json:"importable"`
	Reason          *string `json:"reason"`
	AlreadyImported bool    `json:"already_imported"`
	// Rule is what importing would create (null when not importable).
	Rule *RulePreview `json:"rule"`

	rule rules.Rule
}

// RulePreview is the rule an import would create.
type RulePreview struct {
	Name         string  `json:"name"`
	BusinessID   *string `json:"business_id"`
	MatchToLocal *string `json:"match_to_local"`
	MatchFrom    *string `json:"match_from"`
	MatchSubject *string `json:"match_subject"`
	GmailLabel   string  `json:"gmail_label"`
}

var (
	multiTerm   = regexp.MustCompile(`(?i)\s(or|and)\s|[(){}]|^-|\s-`)
	whitespace  = regexp.MustCompile(`\s`)
	domainLike  = regexp.MustCompile(`^[a-z0-9.-]+\.[a-z]{2,}$`)
	systemLabel = map[string]bool{"INBOX": true, "UNREAD": true, "SENT": true, "TRASH": true, "SPAM": true, "STARRED": true,
		"IMPORTANT": true, "DRAFT": true, "CHAT": true, "CATEGORY_PERSONAL": true, "CATEGORY_SOCIAL": true,
		"CATEGORY_PROMOTIONS": true, "CATEGORY_UPDATES": true, "CATEGORY_FORUMS": true}
)

func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// DescribeFilter classifies raw against the managed businesses. labelNames maps Gmail label id -> name.
func DescribeFilter(raw RawFilter, labelNames map[string]string, businesses []store.Business) FilterInfo {
	c, a := raw.Criteria, raw.Action
	f := FilterInfo{ID: raw.ID, From: strp(c.From), To: strp(c.To), Subject: strp(c.Subject), Forward: strp(a.Forward)}
	var labels []string
	for _, id := range a.AddLabelIDs {
		switch id {
		case "TRASH", "SPAM":
			f.Delete = true
		default:
			if systemLabel[id] {
				continue
			}
			if n, ok := labelNames[id]; ok {
				labels = append(labels, n)
			}
		}
	}
	sort.Strings(labels)
	for _, id := range a.RemoveLabelIDs {
		switch id {
		case "INBOX":
			f.Archive = true
		case "UNREAD":
			f.MarkRead = true
		}
	}
	if len(labels) > 0 {
		f.Label = &labels[0]
	}
	reject := func(reason string) FilterInfo {
		f.Reason = &reason
		return f
	}
	switch {
	case f.Delete:
		return reject(ReasonDeletesMail)
	case a.Forward != "":
		return reject(ReasonForwardsMail)
	case f.Label == nil:
		return reject(ReasonNoLabel)
	case c.Query != "" || c.NegatedQuery != "" || c.HasAttachment || c.Size != 0 || c.SizeComparison != "":
		return reject(ReasonUnsupportedCriteria)
	case c.From == "" && c.To == "" && c.Subject == "":
		return reject(ReasonUnsupportedCriteria)
	}

	r := rules.Rule{Enabled: true, LabelOnly: true, GmailLabel: f.Label}
	if id := raw.ID; id != "" {
		r.ImportedFromFilter = &id
	}
	if c.From != "" {
		from, ok := parseFrom(c.From)
		if !ok {
			return reject(ReasonUnsupportedCriteria)
		}
		r.MatchFrom = &from
	}
	if c.To != "" {
		local, domain, ok := parseTo(c.To)
		if !ok {
			return reject(ReasonUnsupportedCriteria)
		}
		var biz *store.Business
		for i := range businesses {
			if strings.EqualFold(businesses[i].Domain, domain) {
				biz = &businesses[i]
			}
		}
		if biz == nil {
			return reject(ReasonUnmanagedDomain)
		}
		r.BusinessID = &biz.ID
		if local != "" {
			r.MatchToLocal = &local
		}
	}
	if c.Subject != "" {
		sub := strings.TrimSpace(strings.Trim(strings.TrimSpace(c.Subject), `"`))
		if sub == "" || multiTerm.MatchString(sub) {
			return reject(ReasonUnsupportedCriteria)
		}
		pat := regexp.QuoteMeta(sub)
		r.MatchSubject = &pat
	}
	base := *f.Label
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	r.Name = base
	if errs := rules.Validate(r); len(errs) > 0 {
		return reject(ReasonUnsupportedCriteria)
	}
	f.Importable, f.rule = true, r
	pv := &RulePreview{Name: r.Name, MatchToLocal: r.MatchToLocal, MatchFrom: r.MatchFrom, MatchSubject: r.MatchSubject, GmailLabel: *f.Label}
	if r.BusinessID != nil {
		id := r.BusinessID.String()
		pv.BusinessID = &id
	}
	f.Rule = pv
	return f
}

// parseFrom accepts "a@b.com", "@b.com", "*@b.com" and a bare domain "b.com".
func parseFrom(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"`)))
	if s == "" || whitespace.MatchString(s) || multiTerm.MatchString(s) {
		return "", false
	}
	s = strings.TrimPrefix(s, "*")
	switch {
	case strings.Contains(s, "@"):
		return s, true
	case domainLike.MatchString(s):
		return "@" + s, true
	}
	return "", false
}

// parseTo accepts "local@domain", "@domain" and "*@domain".
func parseTo(s string) (local, domain string, ok bool) {
	s = strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"`)))
	if s == "" || whitespace.MatchString(s) || multiTerm.MatchString(s) {
		return "", "", false
	}
	i := strings.LastIndex(s, "@")
	if i < 0 || i == len(s)-1 {
		return "", "", false
	}
	local, domain = s[:i], s[i+1:]
	if local == "*" {
		local = ""
	}
	return local, domain, true
}

// ListFilters reads the Gmail filters and describes each one. Filters already imported as rules are flagged.
func (s *Service) ListFilters(ctx context.Context) ([]FilterInfo, error) {
	raw, err := s.listFiltersAPI(ctx)
	if err != nil {
		return nil, err
	}
	acct, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	names, err := s.refreshLabels(ctx, acct.ID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]string, len(names))
	for n, id := range names {
		byID[id] = n
	}
	biz, err := s.Store.ListBusinesses(ctx)
	if err != nil {
		return nil, err
	}
	imported, err := s.Store.ImportedFilterIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FilterInfo, 0, len(raw))
	for _, r := range raw {
		f := DescribeFilter(r, byID, biz)
		f.AlreadyImported = imported[r.ID]
		out = append(out, f)
	}
	return out, nil
}
