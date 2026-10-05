package shared

import "testing"

func TestIsWhatsAppDatabase(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{`C:\Users\Mahir\Documents\WhatsApp.db`, true},
		{`C:\Users\Mahir\AppData\WhatsApp\Databases\msgstore.db`, true},
		{`C:\Users\Mahir\AppData\WhatsApp\Databases\contacts.sqlite`, false},
		{`C:\Users\Mahir\Documents\notes.db`, false},
		{`C:\Users\Mahir\Documents\whatsapp-export.txt`, false},
	}

	for _, test := range tests {
		if got := IsWhatsAppDatabase(test.path); got != test.want {
			t.Errorf("IsWhatsAppDatabase(%q) = %t, want %t", test.path, got, test.want)
		}
	}
}
