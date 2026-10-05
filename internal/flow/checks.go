package flow

import (
	"errors"
	"regexp"
	"strings"
)

var apiKeyPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// ValidAPIKey returns key in lower case, or an error if it is not 32 hexadecimal characters.
func ValidAPIKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if !apiKeyPattern.MatchString(key) {
		return "", errors.New("API keys must contain exactly 32 hexadecimal characters.")
	}
	return strings.ToLower(key), nil
}

// APIKey checks a pasted API key and shows the problem as an error line.
func APIKey(value string) (string, error) {
	key, err := ValidAPIKey(value)
	if err != nil {
		return "", errors.New("Error: " + err.Error())
	}
	return key, nil
}

// Required rejects an empty entry with message.
func Required(message string) func(string) (string, error) {
	return func(value string) (string, error) {
		if value == "" {
			return "", errors.New(message)
		}
		return value, nil
	}
}

// Username rejects names that cannot be stored in a "user:hash" entry.
func Username(value string) (string, error) {
	if strings.Contains(value, ":") {
		return "", errors.New("Username cannot contain ':'.")
	}
	return value, nil
}

// APIKeyField asks for the API key that service generated.
func APIKeyField(key, service string) Field {
	return Field{Key: key, Prompt: "Paste the " + service + " API key", Check: APIKey}
}

// PasswordField asks for a new password twice.
func PasswordField(key, service string) Field {
	return Field{
		Key:      key,
		Prompt:   service + " password",
		Kind:     Secret,
		Repeat:   "Repeat " + service + " password",
		Mismatch: "Passwords do not match.",
		Check:    Required("Password cannot be empty."),
	}
}

// UsernameField asks for a username, defaulting to admin.
func UsernameField(key, service string) Field {
	return Field{Key: key, Prompt: service + " username", Default: "admin", Check: Username}
}
