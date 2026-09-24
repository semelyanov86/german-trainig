package farewell

import "testing"

func TestRussianFarewellBoundariesAndGermanCompatibility(t *testing.T) {
	phrases := []string{"пока", "до свидания"}
	if !ContainsForLanguage("Ну, пока!", phrases, "ru") || !ContainsForLanguage("До свидания.", phrases, "ru") {
		t.Fatal("Russian farewell missed")
	}
	if ContainsForLanguage("Покажите пример", phrases, "ru") {
		t.Fatal("Russian word fragment ended call")
	}
	if !ContainsForLanguage("irgendwas tschüsschen", []string{"tschüss"}, "de") {
		t.Fatal("German legacy substring behavior changed")
	}
}
