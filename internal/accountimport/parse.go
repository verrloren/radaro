// Package accountimport parses account credentials supplied for bulk import.
package accountimport

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const MaxRows = 500

// Row is one import entry. Number is the JSON array position or CSV line.
// Error contains a validation message and must never contain Secret.
type Row struct {
	Number   int    `json:"-"`
	Platform string `json:"platform"`
	Handle   string `json:"handle"`
	Secret   string `json:"secret"`
	Instance string `json:"instance"`
	Error    string `json:"-"`
}

// Parse accepts a JSON array of objects or CSV with an optional header.
// Invalid individual rows are returned with Error so valid rows can proceed.
func Parse(input string) ([]Row, error) {
	input = strings.TrimSpace(strings.TrimPrefix(input, "\ufeff"))
	if input == "" {
		return nil, errors.New("import text is empty")
	}
	if strings.HasPrefix(input, "[") {
		return parseJSON(input)
	}
	return parseCSV(input)
}

func parseJSON(input string) ([]Row, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return nil, errors.New("invalid JSON array")
	}
	if len(raw) == 0 {
		return nil, errors.New("import has no rows")
	}
	if len(raw) > MaxRows {
		return nil, fmt.Errorf("import exceeds %d rows", MaxRows)
	}
	rows := make([]Row, len(raw))
	for i, item := range raw {
		rows[i].Number = i + 1
		if len(item) == 0 || item[0] != '{' || json.Unmarshal(item, &rows[i]) != nil {
			rows[i].Error = "expected an object with string fields"
			continue
		}
		validate(&rows[i])
	}
	return rows, nil
}

func parseCSV(input string) ([]Row, error) {
	r := csv.NewReader(strings.NewReader(input))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	fields := []string{"platform", "handle", "secret", "instance"}
	rows := []Row{}
	first := true
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("row %d: invalid CSV", len(rows)+1)
		}
		line, _ := r.FieldPos(0)
		if first {
			first = false
			if isHeader(record) {
				fields = make([]string, len(record))
				seen := map[string]bool{}
				for i, field := range record {
					field = strings.ToLower(strings.TrimSpace(field))
					if !knownField(field) || seen[field] {
						return nil, fmt.Errorf("row %d: invalid or duplicate CSV header", line)
					}
					seen[field] = true
					fields[i] = field
				}
				if !seen["platform"] || !seen["secret"] {
					return nil, fmt.Errorf("row %d: CSV header needs platform and secret", line)
				}
				continue
			}
		}
		if len(rows) == MaxRows {
			return nil, fmt.Errorf("import exceeds %d rows", MaxRows)
		}
		row := Row{Number: line}
		if len(record) > len(fields) {
			row.Error = "too many CSV fields"
		} else {
			for i, value := range record {
				switch fields[i] {
				case "platform":
					row.Platform = value
				case "handle":
					row.Handle = value
				case "secret":
					row.Secret = value
				case "instance":
					row.Instance = value
				}
			}
			validate(&row)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, errors.New("import has no rows")
	}
	return rows, nil
}

func isHeader(fields []string) bool {
	hasPlatform := false
	for _, field := range fields {
		field = strings.ToLower(strings.TrimSpace(field))
		if !knownField(field) {
			return false
		}
		if field == "platform" {
			hasPlatform = true
		}
	}
	return hasPlatform
}

func knownField(field string) bool {
	switch field {
	case "platform", "handle", "secret", "instance":
		return true
	}
	return false
}

func validate(row *Row) {
	row.Platform = strings.ToLower(strings.TrimSpace(row.Platform))
	row.Handle = strings.TrimSpace(row.Handle)
	row.Secret = strings.TrimSpace(row.Secret)
	row.Instance = strings.TrimSpace(row.Instance)
	switch {
	case row.Platform == "":
		row.Error = "platform is required"
	case row.Platform != "reddit" && row.Platform != "bluesky" && row.Platform != "mastodon" && row.Platform != "devto":
		row.Error = "unknown platform"
	case row.Platform == "reddit":
		row.Error = "Add another Reddit account through browser sign-in"
	case row.Secret == "":
		row.Error = "secret is required"
	case row.Platform == "bluesky" && row.Handle == "":
		row.Error = "Bluesky handle is required"
	}
}
