package auth

import (
	"testing"
)

func FuzzCheckNoCLISecrets(f *testing.F) {
	seeds := []string{
		"--help",
		"--user=admin",
		"--password=secret",
		"--secret=topsecret",
		"--client_secret=mysec",
		"--key=12345",
		"--cert=/path/to/cert.pem",
		"--cert-key=/path/to/key.pem",
		"--push-cert-key=/path/to/key.pem",
		"-u",
		"-v",
		"--password",
		"secret",
		"arbitrary string",
		"",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, arg string) {
		_ = CheckNoCLISecrets([]string{arg})
	})
}
