// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"errors"
	"net/url"

	gogithub "github.com/google/go-github/v84/github"
)

// SetBaseURL replaces the underlying go-github BaseURL — test-only hook.
func SetBaseURL(c GitHubClient, raw string) error {
	gc, ok := c.(*githubClient)
	if !ok {
		return errors.New("SetBaseURL only works on *githubClient")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	gc.client.BaseURL = u
	_ = gogithub.NewClient // keep import live
	return nil
}
