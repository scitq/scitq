package utils

import (
	"database/sql"

	"github.com/lib/pq"
)

func NullInt32ToPtr(n sql.NullInt32) *int32 {
	if n.Valid {
		return &n.Int32
	}
	return nil
}

func NullInt64ToPtr(n sql.NullInt64) *int64 {
	if n.Valid {
		return &n.Int64
	}
	return nil
}

func NullFloat64ToPtr(n sql.NullFloat64) *float64 {
	if n.Valid {
		return &n.Float64
	}
	return nil
}

func NullStringToString(n sql.NullString) string {
	if n.Valid {
		return n.String
	}
	return ""
}

func NullStringToPtr(n sql.NullString) *string {
	if n.Valid {
		return &n.String
	}
	return nil
}

// StringArrayToSlice converts a pq.StringArray to a []string.
// Returns an empty []string if the input is nil.
func StringArrayToSlice(input pq.StringArray) []string {
	if input == nil {
		return []string{}
	}
	return []string(input)
}
