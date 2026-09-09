package sqlnamespace

import (
	"fmt"
	"strings"
)

// Parts decodes individually quoted identifiers without splitting literal dots.
func Parts(value string) ([]string, error) {
	var parts []string
	for len(strings.TrimSpace(value)) > 0 {
		value = strings.TrimSpace(value)
		var part strings.Builder
		if value[0] == '"' || value[0] == '`' || value[0] == '[' {
			quote := value[0]
			if quote == '[' {
				quote = ']'
			}
			value = value[1:]
			closed := false
			for len(value) > 0 {
				c := value[0]
				value = value[1:]
				if c == quote {
					if len(value) > 0 && value[0] == quote {
						part.WriteByte(c)
						value = value[1:]
						continue
					}
					closed = true
					break
				}
				part.WriteByte(c)
			}
			if !closed {
				return nil, fmt.Errorf("unclosed quoted identifier")
			}
			value = strings.TrimSpace(value)
			if len(value) > 0 && value[0] != '.' {
				return nil, fmt.Errorf("invalid qualified identifier")
			}
		} else {
			end := strings.IndexByte(value, '.')
			if end < 0 {
				end = len(value)
			}
			part.WriteString(strings.TrimSpace(value[:end]))
			value = value[end:]
		}
		if part.Len() == 0 || strings.ContainsAny(part.String(), "\x00\r\n") {
			return nil, fmt.Errorf("empty or invalid identifier")
		}
		parts = append(parts, part.String())
		if len(value) > 0 {
			value = value[1:]
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("empty identifier")
			}
		}
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty identifier")
	}
	return parts, nil
}

func QuoteReference(engine, value string) (string, error) {
	parts, err := Parts(value)
	if err != nil {
		return "", err
	}
	for i, part := range parts {
		parts[i] = Quote(engine, part)
	}
	return strings.Join(parts, "."), nil
}
