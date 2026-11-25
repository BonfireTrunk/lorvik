// Package domain holds Lorvik's entities and the rules that do not depend on
// storage or transport: card state transitions, claims and leases, token
// generation, link reference parsing, and validation.
//
// It imports nothing from server, store, or cli. Those packages depend on it.
package domain
