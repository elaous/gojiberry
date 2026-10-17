package read

import (
	"sort"
	"strings"
)

// The voices a browser offers for reading aloud come with a name and a language tag and little else. This describes
// them in the words a person chooses by (the accent and the gender of the voice) and lists the ones to offer for a
// language, so that a reader can be picked: English to begin with, American or British, a woman's voice or a man's.
// The accent comes from the region of the language tag; the gender from the voice's name, which engines choose
// openly ("... Female", a given name), and is left empty when the name does not say.

// Voice is a voice an engine offers.
type Voice struct {
	Name string
	// Lang is the BCP 47 tag the engine gives it ("en-GB").
	Lang string
	// Local says it runs on the device; a voice that does not may send the text to a service.
	Local bool
	// ID is what an engine is told to pick the voice by; empty means the Name is the ID.
	ID string
}

// Gender values of Info.
const (
	Female = "female"
	Male   = "male"
)

// Info describes a voice for a person.
type Info struct {
	// Language is the language's name in English ("English"), "" for one not listed.
	Language string
	// Accent is "American", "British" ... for the regions listed, else the region code ("" with none).
	Accent string
	// Gender is Female, Male or "" when the name does not say.
	Gender string
}

var languageNames = map[string]string{"en": "English", "de": "German", "fr": "French", "es": "Spanish", "pt": "Portuguese",
	"it": "Italian", "nl": "Dutch", "ja": "Japanese", "zh": "Chinese", "ko": "Korean", "ar": "Arabic", "he": "Hebrew", "ru": "Russian"}

var accentNames = map[string]string{
	"en-us": "American", "en-gb": "British", "en-au": "Australian", "en-ca": "Canadian", "en-in": "Indian",
	"en-ie": "Irish", "en-za": "South African", "en-nz": "New Zealand", "en-sg": "Singapore",
	"pt-br": "Brazilian", "pt-pt": "European", "es-es": "Castilian", "es-mx": "Mexican", "es-ar": "Argentine", "es-us": "US",
	"fr-fr": "France", "fr-ca": "Canadian", "fr-be": "Belgian", "fr-ch": "Swiss", "de-de": "German", "de-at": "Austrian", "de-ch": "Swiss",
}

// Given names engines use for their voices. A name that is in neither list gives no gender.
var femaleNames = words("samantha victoria karen moira tessa fiona serena kate susan zira hazel aria jenny libby sonia natasha clara " +
	"emma amy joanna salli kimberly ivy kendra nicole olivia raveena aditi allison ava zoe paulina monica luciana helena sabina laura " +
	"marie amelie lea vicki anna petra katja hedda female woman " +
	// Piper's voices, named for the speaker of the recording.
	"lessac alba jenny kathleen kristin ljspeech eva kerstin ramona siwis lisa")
var maleNames = words("alex daniel fred oliver thomas george james david mark guy ryan brian eric liam russell geraint matthew joey justin " +
	"kevin arthur jorge diego juan pablo enrique felipe luca hans stefan conrad male man " +
	"ryan alan joe danny john norman bryce kusal thorsten karlsson pavoque gilles tom davefx carlfm claude faber edresson jeff cadu")

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// Describe says what a voice is in words.
func Describe(v Voice) Info {
	lang := strings.ToLower(strings.ReplaceAll(v.Lang, "_", "-"))
	base, region, _ := strings.Cut(lang, "-")
	info := Info{Language: languageNames[base]}
	if a, ok := accentNames[lang]; ok {
		info.Accent = a
	} else if region != "" {
		info.Accent = strings.ToUpper(region)
	}
	female, male := false, false
	for _, w := range strings.FieldsFunc(strings.ToLower(v.Name), func(r rune) bool { return r < 'a' || r > 'z' }) {
		female = female || femaleNames[w]
		male = male || maleNames[w]
	}
	switch {
	case female && !male:
		info.Gender = Female
	case male && !female:
		info.Gender = Male
	}
	return info
}

// Label is how a voice is written in a list: "Samantha (American, female)".
func Label(v Voice) string {
	i := Describe(v)
	var parts []string
	if i.Accent != "" {
		parts = append(parts, i.Accent)
	}
	if i.Gender != "" {
		parts = append(parts, i.Gender)
	}
	if !v.Local {
		parts = append(parts, "online")
	}
	if len(parts) == 0 {
		return v.Name
	}
	return v.Name + " (" + strings.Join(parts, ", ") + ")"
}

// ForLanguage lists the voices to offer for a language tag: those of the same language, the exact tag first, then by
// accent and name; voices that do not run on the device only when allowNet.
func ForLanguage(all []Voice, lang string, allowNet bool) []Voice {
	lang = strings.ToLower(strings.ReplaceAll(lang, "_", "-"))
	base, _, _ := strings.Cut(lang, "-")
	var out []Voice
	for _, v := range all {
		vl := strings.ToLower(strings.ReplaceAll(v.Lang, "_", "-"))
		if vb, _, _ := strings.Cut(vl, "-"); vb != base || base == "" {
			continue
		}
		if !v.Local && !allowNet {
			continue
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ei := strings.EqualFold(out[i].Lang, lang)
		ej := strings.EqualFold(out[j].Lang, lang)
		if ei != ej {
			return ei
		}
		if out[i].Lang != out[j].Lang {
			return out[i].Lang < out[j].Lang
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Find returns the voice with that name among those to offer for lang, if it is one.
func Find(all []Voice, lang, name string, allowNet bool) (Voice, bool) {
	if name == "" {
		return Voice{}, false
	}
	for _, v := range ForLanguage(all, lang, allowNet) {
		if v.Name == name {
			return v, true
		}
	}
	return Voice{}, false
}

// Key is the ID the voice is picked by: its ID, else its Name.
func (v Voice) Key() string {
	if v.ID != "" {
		return v.ID
	}
	return v.Name
}
