package models

import "evidentia/backend/db/generated"

type Document = generated.Document

// Document.DocumentType values (documents_document_type_check).
const (
	DocumentTypeFIR              = "FIR"
	DocumentTypeForensicReport   = "FORENSIC_REPORT"
	DocumentTypePhotoEvidence    = "PHOTO_EVIDENCE"
	DocumentTypeWitnessStatement = "WITNESS_STATEMENT"
	DocumentTypeOther            = "OTHER"
)

// Document.Status values (documents_status_check). There is deliberately
// no "DELETED" value — see the table comment in the migration.
const (
	DocumentStatusActive   = "ACTIVE"
	DocumentStatusArchived = "ARCHIVED"
	DocumentStatusTampered = "TAMPERED"
)
