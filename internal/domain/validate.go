package domain

import (
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
)

var (
	pluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)
	// SemVer core + optional pre-release/build (RE2-safe; path traversal blocked separately).
	versionPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[\w.-]+)?(?:\+[\w.-]+)?$`)
)

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func ValidatePluginID(id string) *ValidationError {
	if !pluginIDPattern.MatchString(id) {
		return &ValidationError{Field: "id", Message: "invalid plugin id format"}
	}
	return nil
}

func ValidateVersion(v string) *ValidationError {
	if !versionPattern.MatchString(v) {
		return &ValidationError{Field: "version", Message: "invalid semver format"}
	}
	if strings.Contains(v, "/") || strings.Contains(v, `\`) || strings.Contains(v, "..") {
		return &ValidationError{Field: "version", Message: "version must be a single storage-key segment"}
	}
	return nil
}

func ValidateManifest(m *Manifest) []ValidationError {
	var errs []ValidationError
	add := func(field, msg string) {
		errs = append(errs, ValidationError{Field: field, Message: msg})
	}

	if m == nil {
		add("manifest", "manifest is required")
		return errs
	}
	if ve := ValidatePluginID(m.ID); ve != nil {
		add("manifest.id", ve.Message)
	}
	if strings.TrimSpace(m.Name) == "" {
		add("manifest.name", "name is required")
	}
	if ve := ValidateVersion(m.Version); ve != nil {
		add("manifest.version", ve.Message)
	}
	if strings.TrimSpace(m.Description) == "" {
		add("manifest.description", "description is required")
	}
	if strings.TrimSpace(m.Author.Name) == "" {
		add("manifest.author.name", "author name is required")
	}
	if m.Author.Email != "" {
		if _, err := mail.ParseAddress(m.Author.Email); err != nil {
			add("manifest.author.email", "invalid email")
		}
	}
	if m.Author.URL != "" {
		if _, err := url.ParseRequestURI(m.Author.URL); err != nil {
			add("manifest.author.url", "invalid url")
		}
	}
	if strings.TrimSpace(m.License) == "" {
		add("manifest.license", "license is required")
	}
	if !ValidCategory(m.Category) {
		add("manifest.category", "invalid category")
	}
	if strings.TrimSpace(m.Compatibility.ReportPortal) == "" {
		add("manifest.compatibility.reportportal", "reportportal compatibility is required")
	}
	if m.Homepage != "" {
		if _, err := url.ParseRequestURI(m.Homepage); err != nil {
			add("manifest.homepage", "invalid url")
		}
	}
	if m.Access == "" {
		m.Access = AccessPublic
	}
	if m.Access != AccessPublic && m.Access != AccessPremium {
		add("manifest.access", "access must be public or premium")
	}
	if m.Access == AccessPremium && strings.TrimSpace(m.ContactURL) == "" {
		add("manifest.contactUrl", "contactUrl is required for premium plugins")
	}
	if m.ContactURL != "" {
		if _, err := url.ParseRequestURI(m.ContactURL); err != nil {
			add("manifest.contactUrl", "invalid url")
		}
	}
	return errs
}

func ValidateCustomerID(id string) *ValidationError {
	if !pluginIDPattern.MatchString(id) {
		return &ValidationError{Field: "customerId", Message: "invalid customer id format"}
	}
	return nil
}
