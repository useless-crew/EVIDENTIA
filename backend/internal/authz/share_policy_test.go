package authz

import (
	"testing"

	"evidentia/backend/internal/models"
)

func TestSharePermissionCovers(t *testing.T) {
	cases := []struct {
		name       string
		permission string
		action     Action
		want       bool
	}{
		{"VIEW covers read", models.SharePermissionView, ActionDocumentRead, true},
		{"VIEW covers download", models.SharePermissionView, ActionDocumentDownload, true},
		{"VIEW covers certificate read", models.SharePermissionView, ActionCertificateRead, true},
		{"VIEW does not cover verify", models.SharePermissionView, ActionDocumentVerify, false},
		{"VIEW does not cover redact", models.SharePermissionView, ActionDocumentRedact, false},
		{"VIEW does not cover reshare", models.SharePermissionView, ActionDocumentShare, false},
		{"VIEW does not cover certificate create", models.SharePermissionView, ActionCertificateCreate, false},
		{"VERIFY covers read", models.SharePermissionVerify, ActionDocumentRead, true},
		{"VERIFY covers download", models.SharePermissionVerify, ActionDocumentDownload, true},
		{"VERIFY covers verify", models.SharePermissionVerify, ActionDocumentVerify, true},
		{"VERIFY does not cover redact", models.SharePermissionVerify, ActionDocumentRedact, false},
		{"VERIFY does not cover reshare", models.SharePermissionVerify, ActionDocumentShare, false},
		{"VERIFY does not cover upload", models.SharePermissionVerify, ActionDocumentUpload, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sharePermissionCovers(tc.permission, tc.action)
			if got != tc.want {
				t.Errorf("sharePermissionCovers(%q, %q) = %v, want %v", tc.permission, tc.action, got, tc.want)
			}
		})
	}
}

func TestShareCoverableAction(t *testing.T) {
	neverCoverable := []Action{
		ActionDocumentRedact,
		ActionDocumentShare,
		ActionCertificateCreate,
		ActionDocumentUpload,
		ActionCaseCreate,
		ActionCaseUpdate,
		ActionUserCreate,
		ActionUserRole,
		ActionAuditVerify,
	}
	for _, action := range neverCoverable {
		if shareCoverableAction(action) {
			t.Errorf("shareCoverableAction(%q) = true, want false — a share must never be able to cover this action regardless of permission tier", action)
		}
	}

	coverable := []Action{ActionDocumentRead, ActionDocumentDownload, ActionCertificateRead, ActionDocumentVerify}
	for _, action := range coverable {
		if !shareCoverableAction(action) {
			t.Errorf("shareCoverableAction(%q) = false, want true", action)
		}
	}
}
