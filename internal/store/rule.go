package store

import (
	"github.com/formancehq/reconciliation/internal/models"
)

// RulePatch is the partial-update payload accepted by PatchRule. Nil fields are
// left unchanged. Mutating template_kind or template_spec requires re-validation
// by the service layer before this is called.
type RulePatch struct {
	Name          *string
	TemplateKind  *models.TemplateKind
	TemplateSpec  []byte
	CompiledCEL   *string
	Enabled       *bool
	Severity      *models.Severity
	Schedule      *models.Schedule
	Notifications *[]string
	Labels        *map[string]string
}
