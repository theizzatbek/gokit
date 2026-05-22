// Package fibermap loads YAML-described HTTP route trees and middleware
// chains into a Fiber router. It exposes a generic per-request context type
// so handlers can read pre-built typed data without re-parsing locals.
//
// Lifecycle: New → SetContextBuilder → RegisterHandler/RegisterMiddleware →
// SetRoleChecker (if any roles are used in YAML) → LoadFile/LoadBytes → Mount.
//
// See the package README and design spec for details.
package fibermap