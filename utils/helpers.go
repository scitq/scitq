package utils

import "database/sql"

func NullInt32ToPtr(n sql.NullInt32) *int32 {
	if n.Valid {
		return &n.Int32
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
