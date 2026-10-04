// Package lang defines the languages the panel renders in, shared by the web UI
// locales, a user's stored preference, and the per-language node names an
// administrator authors.
//
// The empty code is deliberately valid and means "unset": users predate this
// preference, and a node needs a translation only for the languages an operator
// chose to write. Both cases fall back to the untranslated name.
package lang

type Code string

const (
	Korean  Code = "ko"
	English Code = "en"
	Chinese Code = "zh"
	Unset   Code = ""
)

func (c Code) Valid() bool {
	switch c {
	case Korean, English, Chinese, Unset:
		return true
	}
	return false
}

// Translatable reports whether a node name may be authored for this code.
// Unset is valid as a preference but is not a language to translate into.
func (c Code) Translatable() bool {
	return c != Unset && c.Valid()
}

func Codes() []Code {
	return []Code{Korean, English, Chinese}
}
