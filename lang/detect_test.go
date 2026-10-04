package lang

import "testing"

func TestDetect(t *testing.T) {
	cases := map[string]string{
		"en": "The quick brown fox jumps over the lazy dog and it is not that hard to read this sentence for a person with the right tools.",
		"es": "El perro de la casa no quiere que los niños se vayan por la calle con una pelota para jugar en el parque del barrio.",
		"fr": "Le chat de la maison est dans le jardin et il ne veut pas que les enfants du quartier sortent pour jouer sur la route.",
		"de": "Der Hund ist nicht mit dem Kind im Garten und die Katze sitzt auf dem Dach von einem alten Haus in der Stadt.",
		"pt": "O gato da casa não quer que os meninos saiam para a rua com uma bola e por isso fica no jardim com a mãe.",
		"it": "Il gatto di casa non vuole che i bambini escano per la strada con una palla e per questo sono in giardino con la mamma.",
		"nl": "De kat van het huis is niet in de tuin en het kind wil dat er een hond is voor de kinderen van de straat.",
	}
	for want, text := range cases {
		if got, ok := Detect(text); !ok || got != want {
			t.Errorf("%s: got %q %v", want, got, ok)
		}
	}
}

func TestDetectDeclinesWithoutSignal(t *testing.T) {
	for name, text := range map[string]string{
		"too short":    "The cat sat.",
		"no stopwords": "Gojiberry Xangô canvas collab hub transport surface flow header footer objects spell lang detect",
		"empty":        "",
		"numbers only": "1 2 3 4 5 6 7 8 9 10 11 12 13 14",
	} {
		if code, ok := Detect(text); ok {
			t.Errorf("%s: guessed %q", name, code)
		}
	}
	if len(Codes()) != len(Names) {
		t.Error("every language needs a name")
	}
}
