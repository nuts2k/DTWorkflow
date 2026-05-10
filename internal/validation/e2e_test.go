package validation

import "testing"

func TestE2EModule(t *testing.T) {
	tests := []struct {
		name, module string
		wantErr      bool
	}{
		{"empty OK", "", false},
		{"valid module", "order", false},
		{"nested module", "order/sub", false},
		{"absolute path", "/etc/passwd", true},
		{"path traversal", "../secret", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := E2EModule(tt.module)
			if (err != nil) != tt.wantErr {
				t.Errorf("E2EModule(%q) = %v, wantErr %v", tt.module, err, tt.wantErr)
			}
		})
	}
}

func TestE2ECaseName(t *testing.T) {
	tests := []struct {
		name, caseName string
		wantErr        bool
	}{
		{"empty OK", "", false},
		{"valid case", "create-order", false},
		{"with slash", "order/create", true},
		{"path traversal", "..", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := E2ECaseName(tt.caseName)
			if (err != nil) != tt.wantErr {
				t.Errorf("E2ECaseName(%q) = %v, wantErr %v", tt.caseName, err, tt.wantErr)
			}
		})
	}
}

func TestE2EBaseURL(t *testing.T) {
	tests := []struct {
		name, url string
		wantErr   bool
	}{
		{"empty OK", "", false},
		{"valid https", "https://staging.example.com", false},
		{"valid http", "http://localhost:3000", false},
		{"no scheme", "staging.example.com", true},
		{"ftp scheme", "ftp://host", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := E2EBaseURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("E2EBaseURL(%q) = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}
