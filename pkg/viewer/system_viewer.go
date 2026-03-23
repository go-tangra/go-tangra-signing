package viewer

import (
	"context"

	"github.com/tx7do/go-crud/viewer"
)

// SystemViewer describes a system-viewer.
type SystemViewer struct {
}

func NewSystemViewer() viewer.Context {
	return SystemViewer{}
}

func NewSystemViewerContext(ctx context.Context) context.Context {
	return viewer.WithContext(ctx, NewSystemViewer())
}

func (v SystemViewer) ShouldAudit() bool {
	return false
}

func (v SystemViewer) UserID() uint64 {
	return 0
}

func (v SystemViewer) TenantID() uint64 {
	return 0
}

func (v SystemViewer) OrgUnitID() uint64 {
	return 0
}

func (v SystemViewer) Permissions() []string {
	return []string{}
}

func (v SystemViewer) Roles() []string {
	return []string{}
}

func (v SystemViewer) DataScope() []viewer.DataScope {
	return []viewer.DataScope{}
}

func (v SystemViewer) TraceID() string {
	return ""
}

func (v SystemViewer) HasPermission(action, resource string) bool {
	return true
}

func (v SystemViewer) IsPlatformContext() bool {
	return true
}

func (v SystemViewer) IsTenantContext() bool {
	return false
}

func (v SystemViewer) IsSystemContext() bool {
	return true
}
