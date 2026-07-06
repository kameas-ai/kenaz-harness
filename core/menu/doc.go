// Package menu builds the OS-native application menu for the kenaz harness.
//
// # OSS-first contract
//
// This package does NOT import core/fleet. Fleet identity (sign-in / sign-out)
// is entirely absent from the menu bar — those affordances live in the in-window
// UserMenu dropdown per spec FR-005.
//
// # Forking recipe
//
// Removing the native menu bar requires exactly two wire-up points in main.go:
//
//  1. The [*menu.Menu] value passed into options.App.Menu (drop it or set to nil).
//  2. The broker-event subscriptions that call [runtime.MenuSetApplicationMenu]
//     on theme/update/recent-session changes (drop the subscription goroutine).
//
// The UserMenu status pill (avatar + env badge + sign-in/out) is NOT part of
// this package and is unaffected by removing core/menu.
//
// # Dynamic rebuilds
//
// The menu is rebuilt and re-applied via runtime.MenuSetApplicationMenu
// whenever one of these state changes fires via the broker:
//   - theme:changed      → flips the radio checkmark in View → Theme
//   - update:available   → re-labels Help → Check for Updates
//   - sessions:updated   → repopulates File → Open Recent (max 10)
//
// Each rebuild is debounced to ≤ 1 rebuild per 100 ms (FR-012).
package menu
