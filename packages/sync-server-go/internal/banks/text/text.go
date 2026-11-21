// Package text ports the payee-name and title helpers shared by the bank-sync
// providers (packages/sync-server/src/util/payee-name.ts and util/title/*).
package text

import (
	"strconv"
	"strings"
	"unicode"
)

// Transaction is the subset of the GoCardless-style normalized transaction
// used to build a payee name.
type Transaction struct {
	TransactionAmount struct {
		Amount string `json:"amount"`
	} `json:"transactionAmount"`
	DebtorName          string   `json:"debtorName"`
	CreditorName        string   `json:"creditorName"`
	DebtorAccount       *Account `json:"debtorAccount"`
	CreditorAccount     *Account `json:"creditorAccount"`
	RemittanceInfo      string   `json:"remittanceInformationUnstructured"`
	RemittanceInfoArray []string `json:"remittanceInformationUnstructuredArray"`
	AdditionalInfo      string   `json:"additionalInformation"`
}

// Account is the account subset used for the IBAN suffix.
type Account struct {
	IBAN string `json:"iban"`
}

// FormatPayeeName ports formatPayeeName: the positive side uses the debtor,
// the negative side the creditor, with the IBAN appended when available.
func FormatPayeeName(trans Transaction) string {
	amount, _ := strconv.ParseFloat(trans.TransactionAmount.Amount, 64)

	var name string
	var account *Account
	if amount >= 0 {
		name = trans.DebtorName
		account = trans.DebtorAccount
	} else {
		name = trans.CreditorName
		account = trans.CreditorAccount
	}

	if name == "" {
		if trans.DebtorAccount != nil {
			account = trans.DebtorAccount
		} else if trans.CreditorAccount != nil {
			account = trans.CreditorAccount
		}
	}

	if name == "" {
		name = trans.DebtorName
	}
	if name == "" {
		name = trans.CreditorName
	}
	if name == "" {
		name = trans.RemittanceInfo
	}
	if name == "" {
		name = strings.Join(trans.RemittanceInfoArray, ", ")
	}
	if name == "" {
		name = trans.AdditionalInfo
	}

	parts := make([]string, 0, 2)
	if name != "" {
		parts = append(parts, Title(name, nil))
	}
	if account != nil && account.IBAN != "" {
		parts = append(parts, formatIBAN(account.IBAN))
	}

	return strings.Join(parts, " ")
}

func formatIBAN(iban string) string {
	runes := []rune(iban)
	if len(runes) <= 8 {
		return "(" + iban + ")"
	}
	return "(" + string(runes[:4]) + " XXX " + string(runes[len(runes)-4:]) + ")"
}

var lowerCaseWords = func() map[string]bool {
	words := []string{
		// conjunctions
		"for", "and", "nor", "but", "or", "yet", "so",
		// articles
		"a", "an", "the",
		// prepositions
		"aboard", "about", "above", "across", "after", "against", "along",
		"amid", "among", "anti", "around", "as", "at", "before", "behind",
		"below", "beneath", "beside", "besides", "between", "beyond", "but",
		"by", "concerning", "considering", "despite", "down", "during",
		"except", "excepting", "excluding", "following", "for", "from", "in",
		"inside", "into", "like", "minus", "near", "of", "off", "on", "onto",
		"opposite", "over", "past", "per", "plus", "regarding", "round",
		"save", "since", "than", "through", "to", "toward", "towards", "under",
		"underneath", "unlike", "until", "up", "upon", "versus", "via", "with",
		"within", "without",
	}
	set := make(map[string]bool, len(words))
	for _, word := range words {
		set[word] = true
	}
	return set
}()

// canonicalSpecials mirrors util/title/specials.ts.
var canonicalSpecials = []string{
	"CLI", "API", "HTTP", "HTTPS", "JSX", "DNS", "URL", "CI", "CDN",
	"GitHub", "CSS", "JS", "JavaScript", "TypeScript", "HTML", "WordPress",
	"Next.js", "Node.js",
}

// Title lowercases the string, capitalizes words (except the small words from
// util/title/lower-case.ts unless they start a sentence), and restores the
// canonical spelling of known acronyms.
func Title(value string, extraSpecials []string) string {
	lower := strings.ToLower(value)
	runes := []rune(lower)
	var builder strings.Builder
	builder.Grow(len(lower))

	// Forced is true at the start and after sentence punctuation, matching the
	// JS regex's `forced` group.
	forced := true
	for i := 0; i < len(runes); {
		if isWordRune(runes[i]) {
			start := i
			for i < len(runes) && isWordRune(runes[i]) {
				i++
			}
			word := string(runes[start:i])
			if forced || !lowerCaseWords[word] {
				word = capitalize(word)
			}
			builder.WriteString(word)
			forced = false
			continue
		}

		r := runes[i]
		builder.WriteRune(r)
		if !unicode.IsSpace(r) {
			forced = strings.ContainsRune(".!?;:\"()-", r)
		}
		i++
	}

	out := builder.String()
	for _, special := range append(canonicalSpecials, extraSpecials...) {
		out = replaceWord(out, special)
	}
	return out
}

func capitalize(word string) string {
	runes := []rune(word)
	if len(runes) == 0 {
		return word
	}
	return string(unicode.ToUpper(runes[0])) + string(runes[1:])
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’'
}

// replaceWord replaces a whole word case-insensitively with its canonical
// spelling.
func replaceWord(value, replacement string) string {
	lowerValue := strings.ToLower(value)
	lowerReplacement := strings.ToLower(replacement)
	offset := 0
	for {
		index := strings.Index(lowerValue[offset:], lowerReplacement)
		if index < 0 {
			return value
		}
		index += offset
		end := index + len(lowerReplacement)

		beforeOK := index == 0 || !isWordRuneAt(value, index-1)
		afterOK := end >= len(value) || !isWordRuneAt(value, end)
		if beforeOK && afterOK {
			value = value[:index] + replacement + value[end:]
			lowerValue = lowerValue[:index] + strings.ToLower(replacement) + lowerValue[end:]
		}
		offset = end
	}
}

func isWordRuneAt(value string, byteIndex int) bool {
	r := rune(value[byteIndex])
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’'
}
