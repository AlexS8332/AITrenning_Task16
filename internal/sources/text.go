package sources

// Truncate обрезает длинный текст по рунам: статья Википедии целиком не
// нужна ни модели, ни человеку в терминале. Обрыв помечается, иначе
// читатель не узнает, что текста было больше.
func Truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + " …[обрезано]"
}
