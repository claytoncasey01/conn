package settings

import (
	"fmt"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// Section names: the sections a PUT may replace.
const (
	sectionRepos     = "repos"
	sectionGitHub    = "github"
	sectionSlack     = "slack"
	sectionAtlassian = "atlassian"
	sectionReviews   = "reviews"
)

// problemsShownMax is how many problems a refusal names before it counts the rest.
const problemsShownMax = 5

// Checked is a section as a PUT sent it: the normalised value, or why it is refused.
type Checked struct {
	OK    bool   `json:"ok"`
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

func refused(reason string) Checked { return Checked{OK: false, Value: nil, Error: reason} }

// Sections are the settings sections a PUT may replace, in the file's order.
func Sections() []string {
	return []string{sectionRepos, sectionGitHub, sectionSlack, sectionAtlassian, sectionReviews}
}

// ValidateSection checks one section as a PUT sends it, replacing the whole section. Stricter
// than reading the file: a bad entry is refused by name (the first few, all of them in one
// message) rather than dropped, a switch must be a boolean, and the allowlist must hold between
// one and 25 valid patterns. The value is normalised (lowercase, repeats once, defaults filled
// in).
func ValidateSection(name, body any) Checked {
	sectionName, isString := name.(string)
	if !isString {
		return refused("there is no settings section " + show(name))
	}

	if !slices.Contains(Sections(), sectionName) {
		return refused("there is no settings section " + show(name))
	}

	object, isObject := body.(*js.Object)
	if !isObject {
		return refused(sectionName + " must be a JSON object")
	}

	var found problems

	value, refusal := validate(sectionName, object, &found)
	if refusal != "" {
		return refused(refusal)
	}

	if len(found.list) > 0 {
		return refused(summary(found.list))
	}

	return Checked{OK: true, Value: value, Error: ""}
}

// validate reads one section strictly: its value, or a refusal. What it would drop goes to found.
func validate(name string, object *js.Object, found *problems) (any, string) {
	switch name {
	case sectionRepos:
		return validateRepos(object, found)
	case sectionGitHub:
		enabled, isBool := boolAt(object, "enabled")
		if !isBool {
			return nil, "github.enabled must be true or false"
		}

		return GitHub{Enabled: enabled}, ""
	case sectionSlack:
		_, isBool := boolAt(object, "enabled")
		if !isBool {
			return nil, "slack.enabled must be true or false"
		}

		return readSlack(object, true, found), ""
	case sectionAtlassian:
		_, isBool := boolAt(object, "enabled")
		if !isBool {
			return nil, "atlassian.enabled must be true or false"
		}

		return readAtlassian(object, true, nil, false, found), ""
	}

	return validateReviews(object)
}

func validateRepos(object *js.Object, found *problems) (any, string) {
	list, isList := listAt(object, "allow")
	if !isList {
		return nil, "repos.allow must be a list of patterns: choose at least one repository"
	}

	if len(list) > PatternsMax {
		return nil, fmt.Sprintf("repos.allow holds %d patterns: at most %d", len(list), PatternsMax)
	}

	value := readRepos(object, true, found)
	if len(found.list) > 0 {
		return value, ""
	}

	if len(value.Allow) == 0 {
		return nil, "choose at least one repository"
	}

	return value, ""
}

func validateReviews(object *js.Object) (any, string) {
	const refusal = "reviews.requested and reviews.allOpen must each be true or false"

	requested, isBool := boolAt(object, "requested")
	if !isBool {
		return nil, refusal
	}

	allOpen, isBool := boolAt(object, "allOpen")
	if !isBool {
		return nil, refusal
	}

	return Reviews{Requested: requested, AllOpen: allOpen}, ""
}

// boolAt is object's key when it holds a boolean.
func boolAt(object *js.Object, key string) (bool, bool) {
	value, _ := object.Get(key)
	typed, isBool := value.(bool)

	return typed, isBool
}

// listAt is object's key when it holds a list.
func listAt(object *js.Object, key string) ([]any, bool) {
	value, _ := object.Get(key)
	typed, isList := value.([]any)

	return typed, isList
}

// objectAt is object's key when it holds an object.
func objectAt(object *js.Object, key string) (*js.Object, bool) {
	value, _ := object.Get(key)
	typed, isObject := value.(*js.Object)

	return typed, isObject
}

// summary is the first few problems, and how many more there are.
func summary(list []string) string {
	text := strings.Join(list[:min(problemsShownMax, len(list))], "; ")
	if len(list) > problemsShownMax {
		text += fmt.Sprintf("; and %d more", len(list)-problemsShownMax)
	}

	return text
}
