// Package hooks_test — singleton_test.go previously tested InitHooksManager,
// GetManager, and DispatchHook singleton functions.
//
// These tests have been removed as part of the Activity struct DI refactor
// (Revision 3). Hook dispatch is now tested via Activities.DispatchHook in
// the temporal package tests, which receive the Manager via DI.
package hooks_test
