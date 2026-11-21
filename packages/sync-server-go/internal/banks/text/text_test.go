package text

import "testing"

func TestTitle(t *testing.T) {
	cases := map[string]string{
		"the home depot":            "The Home Depot",
		"bank of america":           "Bank of America",
		"GITHUB API":                "GitHub API",
		"node.js project":           "Node.js Project",
		"foo - the bar":             "Foo - The Bar",
		"mcdonald's":                "Mcdonald's",
		"(the foo)":                 "(The Foo)",
		"STARBUCKS #123":            "Starbucks #123",
		"e-commerce":                "E-Commerce",
		"a tale of two cities":      "A Tale of Two Cities",
		"visit https://example.com": "Visit HTTPS://Example.Com",
	}

	for input, want := range cases {
		if got := Title(input, nil); got != want {
			t.Errorf("Title(%q) = %q, want %q", input, got, want)
		}
	}

	if got := Title("my custom term", []string{"Custom"}); got != "My Custom Term" {
		t.Errorf("custom special = %q", got)
	}
}

func TestFormatPayeeName(t *testing.T) {
	transaction := Transaction{}
	transaction.TransactionAmount.Amount = "12.34"
	transaction.DebtorName = "ACME STORE"
	transaction.DebtorAccount = &Account{IBAN: "DE89370400440532013000"}
	if got := FormatPayeeName(transaction); got != "Acme Store (DE89 XXX 3000)" {
		t.Errorf("positive payee = %q", got)
	}

	transaction = Transaction{}
	transaction.TransactionAmount.Amount = "-12.34"
	transaction.CreditorName = "the landlord"
	if got := FormatPayeeName(transaction); got != "The Landlord" {
		t.Errorf("negative payee = %q", got)
	}

	transaction = Transaction{}
	transaction.TransactionAmount.Amount = "-1"
	transaction.RemittanceInfoArray = []string{"first", "second"}
	if got := FormatPayeeName(transaction); got != "First, Second" {
		t.Errorf("fallback payee = %q", got)
	}
}
