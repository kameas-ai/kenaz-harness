# OS Menu Bar — operator & developer guide

**Mission**: `os-menu-bar-01NDFSEX16`  
**Targets**: v0.20.0  
**Status**: shipped

---

## Why this exists

The harness originally exposed all in-app actions through the top-right `UserMenu`
dropdown: Search, Command Palette, Theme, Update Available, plus fleet identity
rows (Sign In/Out, Account Settings). This worked fine as a web-app pattern
but broke native desktop expectations:

1. macOS users look at the OS menu bar for app-level actions (Preferences, Quit,
   About). Not finding them there was a discoverability gap.
2. Keyboard accelerators (⌘F, ⌘K) are discoverable inline in the native menu bar
   — the dropdown showed them but the OS-level discovery loop didn't surface them.

**Separation**: action items live in the OS menu bar; identity/account state lives
in the window chrome (UserMenu dropdown).

---

## Menu structure

```
App menu (macOS only — AppMenuRole → NSApp standard items)
  About Kenaz Harness
  Preferences…     ⌘,  → /settings
  ───────────────
  Hide Kenaz Harness  ⌘H
  Hide Others         ⌥⌘H
  Show All
  ───────────────
  Quit Kenaz Harness  ⌘Q

File
  New Session         ⌘N   → /sessions/new
  Open Recent  ▶      populated from recent sessions (max 10)
  ───────────────
  Close Window        ⌘W
  [Quit ⌘Q]          (Win/Linux only; macOS uses App menu)

Edit
  (macOS: EditMenuRole → NSApp standard items)
  (Win/Linux: Undo ⌘Z / Redo ⇧⌘Z / Cut / Copy / Paste / Select All)

View
  Command Palette     ⌘K   → menu:cmd-palette:open
  Search              ⌘F   → menu:search:open
  ───────────────
  Theme ▶
    ● Light                  → menu:theme:set {mode:"light"}
      Dark                   → menu:theme:set {mode:"dark"}
      System                 → menu:theme:set {mode:"system"}
  ───────────────
  Toggle Cheat Sheet         → menu:cheat-sheet:toggle

Help
  Keyboard Shortcuts         → menu:cheat-sheet:toggle
  Documentation…             → https://docs.kameas.ai
  Report Issue…              → https://github.com/kameas-ai/kenaz-harness/issues/new
  ───────────────
  Check for Updates…         → update.StartCheck (label changes on state)
  ───────────────
  About Kenaz Harness        → menu:about:open (hidden on macOS — in App menu)
```

---

## How menu rebuilds work

The menu is rebuilt and re-applied via `runtime.MenuSetApplicationMenu` whenever
the broker fires these events:

| Event | What changes |
|---|---|
| `theme:changed` | View → Theme radio checkmark |
| `update:available` | Help → "Check for Updates" label |

Rebuilds are debounced to one per 100 ms (spec FR-012).

**Sign-in/out do NOT trigger a menu rebuild.** Per spec FR-005/FR-020, account
state remains entirely in-window (UserMenu dropdown). The menu bar carries no
fleet-bound items.

---

## How to extend — adding a new menu item

1. Add a handler method to `core/menu/handlers.go`:

```go
func (h *Handlers) onMyFeature(_ *wailsmenu.CallbackData) {
    h.publish("menu:my-feature", nil)
}
```

2. Add the item in `core/menu/menu.go` inside the appropriate `buildXxxMenu` func:

```go
sub.AddText("My Feature", keys.CmdOrCtrl("m"), h.onMyFeature)
```

3. Subscribe in `frontend/src/App.vue`:

```ts
useEventStream<null>('menu:my-feature', () => {
  // call the appropriate composable
});
```

4. Add a test case in `core/menu/handlers_test.go` and `core/menu/menu_test.go`.

---

## Fork recipe (removing the native menu bar)

`core/menu/` has exactly **two wire-up points** in `main.go`. Remove them to
ship a harness without the native menu bar:

1. Delete the `appMenu` construction block and the `Menu: appMenu` field from
   `options.App`.
2. Delete the `runtime.EventsOn` subscriptions for `theme:changed` and
   `update:available` in `OnStartup`.
3. Remove the `coremenus` import and the `deriveMenuState` helper.

The UserMenu status pill (avatar + env badge + sign-in/out) is not part of
`core/menu/` and is unaffected.

---

## Per-OS behaviour

### macOS

- Native NSApplicationMenu (App menu) appears at the top of the screen.
- `AppMenu()` role gives the OS-standard About, Preferences, Hide, Quit items.
- `EditMenu()` role gives the OS-standard Undo/Redo/Cut/Copy/Paste/SelectAll.
- About dialog reachable via App → About Kenaz Harness; the Help → About item
  is hidden (macOS HIG: avoid duplication).
- ⌘Q quits via NSApp; Close Window (⌘W) hides the window.

### Windows

- In-window menu bar (Wails renders it as a native Win32 menu).
- Quit lives under File → Quit (Ctrl+Q); no App menu concept.
- Edit items are explicit (no OS-native EditMenuRole fallback on Windows).
- About visible under Help → About Kenaz Harness.

### Linux (GNOME / KDE)

- Wails falls back to an in-window menu bar (GTK3 `GtkMenuBar`).
- GNOME HeaderBar mode hides the global menu; Wails works around this by
  rendering the menu in-window by default.
- Behaviour identical to Windows: Quit under File, About under Help.

---

## Manual cross-platform smoke checklist

### macOS

- [ ] App menu visible at top of screen: About, Preferences (⌘,), Hide (⌘H), Quit (⌘Q)
- [ ] ⌘Q quits the application cleanly
- [ ] ⌘, opens Settings panel
- [ ] App → About opens the About dialog; shows version + build hash
- [ ] View → Search (⌘F) opens the search palette
- [ ] View → Command Palette (⌘K) opens the command palette
- [ ] ⌘F fires exactly once (no double-fire with in-app keydown handler)
- [ ] View → Theme → Dark switches theme; checkmark moves to Dark within 100 ms
- [ ] Help → Check for Updates triggers an update check
- [ ] Help → Documentation opens https://docs.kameas.ai in browser
- [ ] File → Open Recent lists recent sessions (or "No recent sessions")
- [ ] Clicking a recent session navigates there
- [ ] No "Account" / "Sign In" / "Sign Out" items anywhere in the menu bar

### Windows

- [ ] In-window menu bar visible: File / Edit / View / Help
- [ ] Alt key focuses menu bar; arrow keys navigate
- [ ] Alt+F opens File; Ctrl+Q quits
- [ ] File → Close Window minimizes/hides window
- [ ] View → Search (Ctrl+F) opens search palette
- [ ] Ctrl+F fires exactly once (no double-fire)
- [ ] Help → About shows the About dialog
- [ ] Theme submenu checkmark updates within 100 ms

### Linux Ubuntu GNOME

- [ ] In-window menu bar renders (not hidden by HeaderBar)
- [ ] All items reachable via keyboard (Alt → arrow keys)
- [ ] View → Command Palette works
- [ ] Help → About shows the About dialog

---

## OSS-first verification

```bash
bash scripts/ci/check-no-fleet-imports.sh
bash scripts/ci/check-oss-first.sh
```

`core/menu/` imports no `core/fleet/` packages. The UserMenu dropdown handles
all fleet gating independently.

---

## Acceptance criteria status

| # | Criterion | Status |
|---|---|---|
| 1 | macOS smoke: App/File/Edit/View/Help; keyboard nav; a11y | manual gate |
| 2 | Windows smoke: File/Edit/View/Help; Quit under File; Alt+F | manual gate |
| 3 | Linux GNOME/KDE: in-window menu; all items reachable | manual gate |
| 4 | Keyboard parity: ⌘F opens search via menu OR keyboard; no double-fire | manual gate |
| 5 | In-window sign-in: UserMenu Sign in row works | ✓ vitest |
| 6 | HARNESS_FLEET_DISABLED=1: no fleet items in menu | ✓ unit test |
| 7 | Theme submenu: checkmark moves within 100 ms | manual gate |
| 8 | Update flow: Help label changes on state | ✓ unit test |
| 9 | Open Recent: submenu shows 10 most recent | ✓ unit test (empty + populated) |
| 10 | OSS-first CI passes | ✓ check-no-fleet-imports.sh |

> **NOTE**: Native cross-platform smoke (criteria 1-4, 7) is a **manual gate**
> that requires a real .app build. It cannot be executed in this headless CI
> environment. The operator must run the checklist above against a Wails-built
> binary on each target OS before shipping.
