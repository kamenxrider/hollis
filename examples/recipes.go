// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package examples holds the recipes printed by hollis examples.
package examples

import _ "embed"

// Content is also the checked-in guide; the CLI and offline recipe tests read
// these exact bytes rather than maintaining separate command lists.
//
//go:embed recipes.md
var Content string
