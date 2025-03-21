package test

import "strings"

func SanitizeNameForK8s(name string) string {
	replaced := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	maxLength := 63 // RFC 1123 Label Names
	if len(replaced) > maxLength {
		return replaced[:maxLength]
	}

	return replaced
}
