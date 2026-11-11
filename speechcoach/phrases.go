package speechcoach

import (
	"bufio"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// limits stop an imported list growing without bound
const (
	MaxPhrases   = 1000
	MaxPhraseLen = 240 // runes
)

// Set is a list of phrases to say.
type Set struct {
	ID      string   `json:"id"`
	Lang    string   `json:"lang"` // BCP 47 language of the phrases, "en", "de", ...
	Title   string   `json:"title"`
	Phrases []string `json:"phrases"`
}

func ParseSet(id, lang, title, text string) (Set, error) {
	if !utf8.ValidString(text) {
		return Set{}, errors.New("the list is not valid text")
	}
	s := Set{ID: id, Lang: lang, Title: title}
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := cleanLine(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if utf8.RuneCountInString(line) > MaxPhraseLen {
			return Set{}, errors.New("a phrase is longer than 240 characters")
		}
		key := strings.ToLower(line)
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(s.Phrases) == MaxPhrases {
			return Set{}, errors.New("the list has more than 1000 phrases")
		}
		s.Phrases = append(s.Phrases, line)
	}
	if err := sc.Err(); err != nil {
		return Set{}, err
	}
	if len(s.Phrases) == 0 {
		return Set{}, errors.New("the list has no phrase")
	}
	return s, nil
}

func cleanLine(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}

// Builtin returns the built-in phrase sets.
func Builtin() []Set {
	return []Set{
		{ID: "everyday-en", Lang: "en", Title: "Everyday requests", Phrases: []string{
			"Good morning, how are you today?",
			"Could you please turn the light on?",
			"I would like a glass of water.",
			"Can you call my sister for me?",
			"The meeting starts at ten o'clock.",
			"Please open the window a little.",
			"I need to take my medicine after lunch.",
			"Thank you very much for your help.",
			"What time does the bus leave?",
			"Let's go for a walk in the park.",
			"My favourite colour is green.",
			"Please read the last paragraph again.",
		}},
		{ID: "everyday-de", Lang: "de", Title: "Alltägliche Bitten", Phrases: []string{
			"Guten Morgen, wie geht es Ihnen heute?",
			"Könnten Sie bitte das Licht einschalten?",
			"Ich möchte ein Glas Wasser.",
			"Kannst du bitte meine Schwester anrufen?",
			"Die Besprechung beginnt um zehn Uhr.",
			"Bitte öffnen Sie das Fenster ein wenig.",
			"Ich muss nach dem Mittagessen meine Medizin nehmen.",
			"Vielen Dank für Ihre Hilfe.",
			"Wann fährt der Bus ab?",
			"Lass uns im Park spazieren gehen.",
			"Meine Lieblingsfarbe ist Grün.",
			"Bitte lesen Sie den letzten Absatz noch einmal vor.",
		}},
		{ID: "everyday-fr", Lang: "fr", Title: "Demandes de tous les jours", Phrases: []string{
			"Bonjour, comment allez-vous aujourd'hui ?",
			"Pourriez-vous allumer la lumière, s'il vous plaît ?",
			"Je voudrais un verre d'eau.",
			"Peux-tu appeler ma sœur pour moi ?",
			"La réunion commence à dix heures.",
			"Ouvrez un peu la fenêtre, s'il vous plaît.",
			"Je dois prendre mon médicament après le déjeuner.",
			"Merci beaucoup pour votre aide.",
			"À quelle heure part le bus ?",
			"Allons nous promener dans le parc.",
			"Ma couleur préférée est le vert.",
			"Relisez le dernier paragraphe, s'il vous plaît.",
		}},
		{ID: "everyday-es", Lang: "es", Title: "Peticiones de cada día", Phrases: []string{
			"Buenos días, ¿cómo está usted hoy?",
			"¿Podría encender la luz, por favor?",
			"Quisiera un vaso de agua.",
			"¿Puedes llamar a mi hermana por mí?",
			"La reunión empieza a las diez.",
			"Abra un poco la ventana, por favor.",
			"Tengo que tomar mi medicina después de comer.",
			"Muchas gracias por su ayuda.",
			"¿A qué hora sale el autobús?",
			"Vamos a dar un paseo por el parque.",
			"Mi color favorito es el verde.",
			"Por favor, lea otra vez el último párrafo.",
		}},
	}
}

func SetFor(lang string) Set {
	if i := strings.IndexAny(lang, "-_"); i >= 0 {
		lang = lang[:i]
	}
	lang = strings.ToLower(lang)
	sets := Builtin()
	for _, s := range sets {
		if s.Lang == lang {
			return s
		}
	}
	return sets[0]
}
