// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"

	"github.com/bborbe/github-release-watcher/pkg/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// generateTestPEM produces a fresh 2048-bit RSA PEM block. Generated per-test
// so the key never expires and never collides with anything real.
func generateTestPEM() []byte {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

var _ = Describe("auth.ResolveGitHubClient", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns error when App not configured", func() {
		client, err := auth.ResolveGitHubClient(ctx, auth.Credentials{})
		Expect(err).To(HaveOccurred())
		Expect(client).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("not configured"))
		Expect(err.Error()).To(ContainSubstring("APP_ID"))
	})

	It("returns error on partial App config — missing PEM_KEY", func() {
		client, err := auth.ResolveGitHubClient(ctx, auth.Credentials{
			AppID:          123,
			InstallationID: 456,
		})
		Expect(err).To(HaveOccurred())
		Expect(client).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("partial GitHub App config"))
		Expect(err.Error()).To(ContainSubstring("PEM_KEY"))
	})

	It("returns error on partial App config — missing APP_ID", func() {
		client, err := auth.ResolveGitHubClient(ctx, auth.Credentials{
			InstallationID: 456,
			PEMKey:         []byte("pem"),
		})
		Expect(err).To(HaveOccurred())
		Expect(client).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("APP_ID"))
	})

	It("returns error on partial App config — missing INSTALLATION_ID", func() {
		client, err := auth.ResolveGitHubClient(ctx, auth.Credentials{
			AppID:  123,
			PEMKey: []byte("pem"),
		})
		Expect(err).To(HaveOccurred())
		Expect(client).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("INSTALLATION_ID"))
	})

	It("returns App-backed client when all three App fields set with valid PEM", func() {
		// githubapp.NewClient does no network I/O during construction; the
		// JWT signing setup just validates the PEM parses.
		client, err := auth.ResolveGitHubClient(ctx, auth.Credentials{
			AppID:          123,
			InstallationID: 456,
			PEMKey:         generateTestPEM(),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(client).NotTo(BeNil())
	})

	It("returns error when App PEM is malformed", func() {
		client, err := auth.ResolveGitHubClient(ctx, auth.Credentials{
			AppID:          123,
			InstallationID: 456,
			PEMKey:         []byte("not-a-valid-pem"),
		})
		Expect(err).To(HaveOccurred())
		Expect(client).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("create github app client"))
	})
})
