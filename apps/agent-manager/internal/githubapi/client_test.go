package githubapi

import (
	"errors"
	"testing"
)

func TestNewClientRequiresToken(t *testing.T) {
	if _, err := NewClient("github.com", ""); err == nil {
		t.Fatalf("expected an error for an empty token")
	} else if !errors.Is(err, ErrAuthenticationRequired) {
		t.Fatalf("error = %v, want ErrAuthenticationRequired", err)
	}
}

func TestNewClientDefaultsToGitHubCom(t *testing.T) {
	client, err := NewClient("", "token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.Host != "github.com" {
		t.Fatalf("Host = %q, want github.com", client.Host)
	}
	if client.rest.BaseURL.String() != "https://api.github.com/" {
		t.Fatalf("BaseURL = %q", client.rest.BaseURL.String())
	}
}

func TestNewClientEnterpriseHost(t *testing.T) {
	client, err := NewClient("github.example.com", "token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.Host != "github.example.com" {
		t.Fatalf("Host = %q", client.Host)
	}
	if client.rest.BaseURL.String() != "https://github.example.com/api/v3/" {
		t.Fatalf("BaseURL = %q", client.rest.BaseURL.String())
	}
}
