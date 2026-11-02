// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/bborbe/github-release-watcher/pkg"
	"github.com/bborbe/github-release-watcher/pkg/build"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// dockerConfig builds a dockerconfigjson payload for the given auths map.
func dockerConfig(auths map[string]map[string]string) []byte {
	raw, err := json.Marshal(map[string]any{"auths": auths})
	Expect(err).NotTo(HaveOccurred())
	return raw
}

// basicAuth builds the `auth` field docker writes: base64("user:password").
func basicAuth(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

var _ = Describe("BuildCredentialReader", func() {
	var (
		ctx    context.Context
		reader build.CredentialReader
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	// newReader returns a reader backed by a clientset holding one Secret.
	// A nil payload omits the Secret entirely.
	newReader := func(payload []byte) build.CredentialReader {
		clientset := fake.NewSimpleClientset()
		if payload != nil {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "buildkit-push",
					Namespace: "build",
				},
				Type: corev1.SecretTypeDockerConfigJson,
				Data: map[string][]byte{".dockerconfigjson": payload},
			}
			_, err := clientset.CoreV1().Secrets("build").Create(
				ctx, secret, metav1.CreateOptions{},
			)
			Expect(err).NotTo(HaveOccurred())
		}
		return pkg.NewBuildCredentialReader(clientset)
	}

	It("reads the legacy index.docker.io key docker login writes", func() {
		// This is the key a real `docker login` produces, and the one the
		// rendered Job's mounted secret carries.
		reader = newReader(dockerConfig(map[string]map[string]string{
			"https://index.docker.io/v1/": {"auth": basicAuth("robot", "s3cret")},
		}))

		creds, err := reader.Credentials(
			ctx, "build", "buildkit-push", "docker.io/bborbe/example:v1.2.3",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(creds.Username).To(Equal("robot"))
		Expect(creds.Password).To(Equal("s3cret"))
	})

	It("reads the short docker.io key too", func() {
		reader = newReader(dockerConfig(map[string]map[string]string{
			"docker.io": {"username": "robot", "password": "s3cret"},
		}))

		creds, err := reader.Credentials(
			ctx, "build", "buildkit-push", "docker.io/bborbe/example:v1.2.3",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(creds.Username).To(Equal("robot"))
		Expect(creds.Password).To(Equal("s3cret"))
	})

	It("reads a registry host that carries a port", func() {
		reader = newReader(dockerConfig(map[string]map[string]string{
			"docker.prod.nuke.benjamin-borbe.de:443": {
				"auth": basicAuth("robot", "s3cret"),
			},
		}))

		creds, err := reader.Credentials(
			ctx, "build", "buildkit-push",
			"docker.prod.nuke.benjamin-borbe.de:443/bborbe/example:v1.2.3",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(creds.Username).To(Equal("robot"))
		Expect(creds.Password).To(Equal("s3cret"))
	})

	It("returns zero credentials when the Secret is absent", func() {
		// A repo with no push secret must keep probing anonymously rather than
		// failing the dispatch — that is the behaviour every caller had before.
		reader = newReader(nil)

		creds, err := reader.Credentials(
			ctx, "build", "buildkit-push", "docker.io/bborbe/example:v1.2.3",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(creds.IsZero()).To(BeTrue())
	})

	It("returns zero credentials when no auth entry names the registry", func() {
		reader = newReader(dockerConfig(map[string]map[string]string{
			"ghcr.io": {"auth": basicAuth("robot", "s3cret")},
		}))

		creds, err := reader.Credentials(
			ctx, "build", "buildkit-push", "docker.io/bborbe/example:v1.2.3",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(creds.IsZero()).To(BeTrue())
	})

	It("returns zero credentials when the Secret carries no dockerconfigjson key", func() {
		clientset := fake.NewSimpleClientset()
		_, err := clientset.CoreV1().Secrets("build").Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "buildkit-push", Namespace: "build"},
			Data:       map[string][]byte{"other": []byte("x")},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		creds, err := pkg.NewBuildCredentialReader(clientset).Credentials(
			ctx, "build", "buildkit-push", "docker.io/bborbe/example:v1.2.3",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(creds.IsZero()).To(BeTrue())
	})

	It("never echoes the payload when the dockerconfigjson cannot be decoded", func() {
		// The payload IS the credential; a decode error that quotes it would
		// put the secret in a log line and a task file.
		reader = newReader([]byte("s3cret-not-json"))

		_, err := reader.Credentials(
			ctx, "build", "buildkit-push", "docker.io/bborbe/example:v1.2.3",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("s3cret-not-json"))
	})
})
