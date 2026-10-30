package db

import "strings"

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EscapeLike escapes the ILIKE wildcards (%, _ and \) so they are searched as literal text:
// searching "50%" must find "Discount 50%", not anything that starts with "50".
func EscapeLike(s string) string {
	return likeEscaper.Replace(s)
}
