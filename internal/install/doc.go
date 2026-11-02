// Package install holds integration tests for the install.sh release installer
// (IMP-IMG-007). It has no production code: install.sh is a standalone shell
// script at the repository root. The tests run that script against a stub HTTP
// server that mimics the GitHub releases redirect and asset endpoints.
package install
