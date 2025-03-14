package test

import "strings"

func GenerateNamespace(testName string) string {
	replaced := strings.ToLower(strings.ReplaceAll(testName, " ", "-"))
	maxLength := 63 // RFC 1123 Label Names
	if len(replaced) > maxLength {
		return replaced[:maxLength]
	}

	return replaced
}
