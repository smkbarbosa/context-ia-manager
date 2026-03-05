// Package docs manages ADR, PRD, and plan documents for ciam projects.
// Templates are embedded in the binary via //go:embed so no external files
// are required at runtime.
package docs

import _ "embed"

//go:embed templates/adr.md
var adrTemplate string

//go:embed templates/prd.md
var prdTemplate string

//go:embed templates/plan.md
var planTemplate string
