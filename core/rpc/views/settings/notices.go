package settings

// Member notices (kenaz-fleet spec 017 T23, contract-harness-sync §17): the
// memory-access and Agent Telemetry texts legal registers for the
// kenaz-harness surface, and whether this member has read them in their
// current org.
//
// Dormant by default: until Fleet serves a text, Notices is empty and the
// panels show nothing.

import (
	"context"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// Plain-wording refusals for the "I have read this" and withdraw controls.
// They never mention legal registration (§17.4 item 4).
var (
	ErrNoticeAckOrgPaused   = errors.New("your organization's Fleet features are paused, so this can't be recorded right now. Try again once they're back")
	ErrNoticeAckOrgInactive = errors.New("your organization's Fleet subscription isn't active, so this can't be recorded right now")
	ErrNoticeAckNotRecorded = errors.New("this couldn't be recorded right now. Memory sync stays paused until it is. Please try again later")
	ErrNoticeChanged        = errors.New("this notice has changed since it was shown. Please read the current text")
	ErrNoticeUnsupported    = errors.New("this notice can't be answered here")
)

// noticeFamilies are the families the harness shows, in display order.
var noticeFamilies = []string{fleet.NoticeFamilyMemoryAccess, fleet.NoticeFamilyAgentTelemetry}

// MemberNoticeView is one served text and this member's state for its family.
type MemberNoticeView struct {
	Family string `json:"family"`
	// Edition, Text and TextHash are exactly what Fleet served (the hash has
	// been checked). Text is shown verbatim.
	Edition  string `json:"edition"`
	Text     string `json:"text"`
	TextHash string `json:"textHash"`
	// Action is the latest row's action in this org: "ack", "withdraw", or
	// "" for no row.
	Action string `json:"action"`
	// RowEdition is the edition that row names ("" for no row).
	RowEdition string `json:"rowEdition"`
	// Acked: the member stands acked at or above the family's minimum.
	Acked      bool   `json:"acked"`
	ActedAt    string `json:"actedAt"`
	AckedSince string `json:"ackedSince"`
}

// MemberNoticesView lists the texts Fleet serves to this member. Empty while
// nothing is registered.
type MemberNoticesView struct {
	Notices []MemberNoticeView `json:"notices"`
	// StateError is set when the texts loaded but the read state didn't.
	StateError string `json:"stateError,omitempty"`
}

// FleetMemberNotices implements SettingsAPI.
func (a *API) FleetMemberNotices(ctx context.Context) (MemberNoticesView, error) {
	out := MemberNoticesView{Notices: []MemberNoticeView{}}
	c := a.fleetClient()
	if c == nil || c.IsNop() || fleet.Disabled() {
		return out, nil
	}
	for _, family := range noticeFamilies {
		nt, err := c.GetNoticeText(ctx, family, "")
		switch {
		case errors.Is(err, fleet.ErrNoticeNotRegistered):
			continue
		case err != nil:
			// A refused hash or a failed read shows nothing: never words
			// Fleet didn't serve, or that didn't match their hash.
			logging.L().Warn("settings.notices.text_unavailable", "family", family, "err", err.Error())
			continue
		}
		out.Notices = append(out.Notices, MemberNoticeView{
			Family: nt.Family, Edition: nt.Edition, Text: nt.Text, TextHash: nt.TextHash,
		})
	}
	if len(out.Notices) == 0 {
		return out, nil
	}
	rows, err := c.GetNoticeAcks(ctx)
	if err != nil {
		logging.L().Warn("settings.notices.acks_unavailable", "err", err.Error())
		out.StateError = "Couldn't check whether you've read this. It will be checked again."
		return out, nil
	}
	for i := range out.Notices {
		n := &out.Notices[i]
		r, ok := fleet.FindNoticeAck(rows, n.Family)
		if !ok {
			continue
		}
		n.Action, n.RowEdition, n.Acked, n.ActedAt = r.Action, r.Edition, r.Acked, r.ActedAt
		if r.AckedSince != nil {
			n.AckedSince = *r.AckedSince
		}
	}
	return out, nil
}

// FleetRespondMemberNotice implements SettingsAPI: records "I have read this"
// (action "ack") or a withdraw for the memory-access text the panel showed.
// edition and textHash must be what Fleet serves now; they are sent back
// unchanged. The Agent Telemetry family is answered through the opt-in PUT
// instead (§17.2).
func (a *API) FleetRespondMemberNotice(ctx context.Context, family, edition, textHash, action string) (MemberNoticesView, error) {
	if family != fleet.NoticeFamilyMemoryAccess {
		return MemberNoticesView{}, ErrNoticeUnsupported
	}
	if action != fleet.NoticeActionAck && action != fleet.NoticeActionWithdraw {
		return MemberNoticesView{}, ErrNoticeUnsupported
	}
	c := a.fleetClient()
	if c == nil || c.IsNop() || fleet.Disabled() {
		return MemberNoticesView{}, fleet.ErrFleetDisabled
	}
	// Only a text Fleet serves now, with the hash the panel showed: the ack
	// names the exact words on screen.
	nt, err := c.GetNoticeText(ctx, family, edition)
	switch {
	case errors.Is(err, fleet.ErrNoticeNotRegistered):
		return MemberNoticesView{}, ErrNoticeChanged
	case err != nil:
		return MemberNoticesView{}, err
	case nt.TextHash != textHash:
		return MemberNoticesView{}, ErrNoticeChanged
	}
	if _, err := c.PostNoticeAck(ctx, nt, action); err != nil {
		switch {
		case fleet.IsOrgPaused(err):
			return MemberNoticesView{}, ErrNoticeAckOrgPaused
		case errors.Is(err, fleet.ErrNoticeOrgInactive):
			return MemberNoticesView{}, ErrNoticeAckOrgInactive
		case errors.Is(err, fleet.ErrNoticeNotRegistered):
			// §17.4 item 4: not recorded. Keep holding, log, no legal
			// wording to the member.
			logging.L().Warn("settings.notices.ack_not_registered", "family", family, "edition", edition, "action", action)
			return MemberNoticesView{}, ErrNoticeAckNotRecorded
		}
		return MemberNoticesView{}, err
	}
	if ms := a.memorySync.Load(); ms != nil {
		ms.InvalidateNoticeHold() // re-checked (and kicked) next cycle
	}
	return a.FleetMemberNotices(ctx)
}
