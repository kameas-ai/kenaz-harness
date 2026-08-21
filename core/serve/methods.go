package serve

import (
	"fmt"
	"sort"
	"strings"
)

// The served surface, as data.
//
// The switch in Server.dispatch is the executable source of truth for which
// methods POST /rpc answers. This file mirrors that switch as a plain list so
// the server can do two things the switch alone cannot:
//
//  1. Tell a caller who typed a method name wrong that they typed it wrong —
//     and what the right spelling is — instead of claiming the method exists
//     on the desktop app but "is not ported to served mode".
//  2. Enumerate the surface over the wire (Serve_ListMethods), so an operator
//     debugging a workbench can discover the method names from the running
//     binary rather than from a source checkout they may not have.
//
// TestServedMethodsMatchDispatchSwitch parses server.go and fails if these
// lists ever drift from the switches, so the mirror cannot silently rot.
//
// Why this exists at all: the old default branch answered EVERY unrecognised
// method with "not ported to served mode; use the desktop app or file a
// ticket". Its own comment claimed that error let callers "distinguish a
// genuine typo" from an unported binding — but the code drew no such
// distinction. A probe that spelled LLM_ListProviders as "llm.listProviders"
// therefore got back a confident, wrong diagnosis: that the running binary
// predated the commit which added the method. Chasing that phantom (stale
// image? stale Nix source? silent binary fallback?) cost hours. The method
// was there the whole time; only the name was wrong.
var (
	// servedMethods are the method names POST /rpc dispatches.
	servedMethods = []string{
		"AppInfo",
		"Auth_State",
		"Config_GetFlags",
		"Connectors_List",
		"Confirm_ApproveBatch",
		"Confirm_CancelBatch",
		"Confirm_ListPending",
		"Confirm_Resolve",
		"Confirm_ResolveAlways",
		"Connectors_Status",
		"Elicit_ListPending",
		"Elicit_SubmitAnswer",
		"LLM_ListProviders",
		"LLM_StartStream",
		"LLM_StopStream",
		"Permissions_ListPending",
		"Permissions_Resolve",
		"Projects_List",
		"Serve_ListMethods",
		"Sessions_AppendMessage",
		"Sessions_Create",
		"Sessions_Delete",
		"Sessions_Get",
		"Sessions_GetUsage",
		"Sessions_List",
		"Sessions_ListMessages",
		"Sessions_ListMessagesActive",
		"Sessions_ListMessagesAll",
		"Sessions_LoadDraft",
		"Sessions_LoadScrollPosition",
		"Sessions_MoveToProject",
		"Sessions_Rename",
		"Sessions_ResolveAutonomy",
		"Sessions_SaveDraft",
		"Sessions_SaveScrollPosition",
		"Sessions_SendMessageWithBlocks",
		"Sessions_SetSystemPrompt",
		"Sessions_SuggestTitle",
		"ShellStatus",
	}

	// servedStreamMethods are answered over GET /ws, not POST /rpc. They are
	// listed here so that posting one to /rpc produces "use /ws" rather than
	// a bare "unknown method" — a caller who knows the name has already got
	// the hard part right.
	servedStreamMethods = []string{
		"Sessions_Stream",
	}
)

// MethodList is the Serve_ListMethods result: the served surface, split by
// transport. Callers use it to discover exact spellings from a running
// binary.
type MethodList struct {
	RPC    []string `json:"rpc"`
	Stream []string `json:"stream"`
}

func methodList() MethodList {
	return MethodList{
		RPC:    append([]string(nil), servedMethods...),
		Stream: append([]string(nil), servedStreamMethods...),
	}
}

// normalizeMethodName reduces a method name to lowercase alphanumerics so
// that spelling variants a caller might reasonably produce collapse onto the
// canonical name:
//
//	"llm.listProviders" → "llmlistproviders"
//	"LLM_ListProviders" → "llmlistproviders"
//	"llm-list-providers" → "llmlistproviders"
//	"app.info"           → "appinfo"
//	"AppInfo"            → "appinfo"
//
// This deliberately ignores separators and case only. It does NOT do edit
// distance: a suggestion is offered only when the caller named the right
// method in the wrong style, never when they named a different method. A
// wrong suggestion here would reintroduce exactly the misdiagnosis this file
// exists to prevent.
func normalizeMethodName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		}
	}
	return b.String()
}

// suggestMethod finds the canonical served method a caller meant, if any.
// transport is "rpc" or "stream".
func suggestMethod(method string) (canonical, transport string, ok bool) {
	norm := normalizeMethodName(method)
	if norm == "" {
		return "", "", false
	}
	for _, m := range servedMethods {
		if normalizeMethodName(m) == norm {
			return m, "rpc", true
		}
	}
	for _, m := range servedStreamMethods {
		if normalizeMethodName(m) == norm {
			return m, "stream", true
		}
	}
	return "", "", false
}

// unknownMethodError builds the error the dispatch default branch returns.
//
// Three distinct outcomes, because they call for three different actions
// from whoever reads the message:
//
//   - Right method, wrong spelling → fix the spelling. Say so, and give the
//     exact name.
//   - Right method, wrong transport → move the call to /ws.
//   - Genuinely not in the surface → the method may exist on the desktop app
//     and not be ported. Only in THIS case is "file a ticket" the right
//     advice, and only here do we say anything about the desktop app.
func unknownMethodError(method string) error {
	if canonical, transport, ok := suggestMethod(method); ok {
		switch transport {
		case "stream":
			return fmt.Errorf(
				"serve: %q is a streaming method — call %q over GET /ws, not POST /rpc",
				method, canonical)
		default:
			return fmt.Errorf(
				"serve: unknown RPC method %q — did you mean %q? Served method names are "+
					"case-sensitive Verb_Noun (call \"Serve_ListMethods\" to enumerate them)",
				method, canonical)
		}
	}
	return fmt.Errorf(
		"serve: unknown RPC method %q — it is not one of the %d methods in the served "+
			"surface (call \"Serve_ListMethods\" to enumerate them). If this method exists "+
			"in the desktop app, it is not ported to served mode; file a ticket",
		method, len(servedMethods))
}

func init() {
	// Cheap invariant: the wire-visible lists are sorted, so Serve_ListMethods
	// output and the drift-guard test have a stable order.
	sort.Strings(servedMethods)
	sort.Strings(servedStreamMethods)
}
