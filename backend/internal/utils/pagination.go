package utils

// DefaultPageSize and MaxPageSize bound every paginated list endpoint in
// this project. There is no project-documented limit to inherit, so these
// are a sensible security default (master prompt: "use sensible security
// limits where no project limit exists") — never allow a client to request
// an unbounded number of rows in one response.
const (
	DefaultPageSize int32 = 20
	MaxPageSize     int32 = 100
)

// Pagination is a validated (page, page_size) pair, plus the LIMIT/OFFSET
// it maps to. Construct only via ParsePagination — it never allows
// PageSize outside [1, MaxPageSize] or Page below 1.
type Pagination struct {
	Page     int32
	PageSize int32
}

// ParsePagination clamps raw, client-supplied page/pageSize values (0
// means "not supplied" — a missing query parameter) into a safe range
// rather than rejecting them: an oversized page_size is silently capped at
// MaxPageSize, a non-positive page or page_size falls back to its default.
// This mirrors this project's existing "shape-only" validation convention
// (see loginRequest) — pagination parameters are a convenience, not a
// security boundary that needs to reject malformed input outright.
func ParsePagination(page, pageSize int32) Pagination {
	if page < 1 {
		page = 1
	}
	switch {
	case pageSize < 1:
		pageSize = DefaultPageSize
	case pageSize > MaxPageSize:
		pageSize = MaxPageSize
	}
	return Pagination{Page: page, PageSize: pageSize}
}

// Limit and Offset are the SQL LIMIT/OFFSET values for this page — every
// paginated repository query takes these, never a raw client-supplied
// value.
func (p Pagination) Limit() int32 { return p.PageSize }

func (p Pagination) Offset() int32 { return (p.Page - 1) * p.PageSize }

// Meta is the pagination metadata block returned alongside a paginated
// list response's data. total is the caller's authorized+filtered row
// count (e.g. from a CountXFiltered query run under the same RLS
// identity), never an unfiltered/unauthorized table count.
type Meta struct {
	Page       int32 `json:"page"`
	PageSize   int32 `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int32 `json:"total_pages"`
}

// BuildMeta computes the Meta block for this page given total.
func (p Pagination) BuildMeta(total int64) Meta {
	var totalPages int32
	if total > 0 {
		totalPages = int32((total + int64(p.PageSize) - 1) / int64(p.PageSize))
	}
	return Meta{Page: p.Page, PageSize: p.PageSize, Total: total, TotalPages: totalPages}
}
