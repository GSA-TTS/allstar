// Copyright 2021 Allstar Authors

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

//     http://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/ossf/allstar/pkg/config/operator"
)

type instLoc struct {
	Exists bool
	Repo   string
	Path   string
}

var (
	instLocs map[string]*instLoc
	mMutex   sync.RWMutex
)

// repoConfigCacheEntry holds a memoized config fetch result. miss is true when
// the config file was absent (404), so callers can skip refetching.
type repoConfigCacheEntry struct {
	json []byte
	miss bool
}

var (
	repoConfigCache  map[string]repoConfigCacheEntry
	repoConfigCacheM sync.RWMutex
)

// repoConfigCacheKey builds a stable key for a resolved config fetch.
func repoConfigCacheKey(owner, repo, name string, cl ConfigLevel) string {
	var b strings.Builder
	b.WriteString(owner)
	b.WriteByte('\x00')
	b.WriteString(repo)
	b.WriteByte('\x00')
	b.WriteString(name)
	b.WriteByte('\x00')
	b.WriteByte(byte('0' + cl))
	return b.String()
}

// getRepoConfigCache returns the cached JSON (ok=true) and whether the entry
// represents an absent config (miss=true).
func getRepoConfigCache(key string) (json []byte, ok bool, miss bool) {
	repoConfigCacheM.RLock()
	defer repoConfigCacheM.RUnlock()
	if repoConfigCache == nil {
		return nil, false, false
	}
	e, ok := repoConfigCache[key]
	return e.json, ok, e.miss
}

func setRepoConfigCache(key string, json []byte) {
	repoConfigCacheM.Lock()
	defer repoConfigCacheM.Unlock()
	if repoConfigCache == nil {
		repoConfigCache = make(map[string]repoConfigCacheEntry)
	}
	repoConfigCache[key] = repoConfigCacheEntry{json: json}
}

func setRepoConfigCacheMiss(key string) {
	repoConfigCacheM.Lock()
	defer repoConfigCacheM.Unlock()
	if repoConfigCache == nil {
		repoConfigCache = make(map[string]repoConfigCacheEntry)
	}
	repoConfigCache[key] = repoConfigCacheEntry{miss: true}
}

// ClearRepoConfigCache drops all memoized config fetches for a repo. It should
// be called once per repo after all policies have run so the next repo (and
// the next reconciliation pass) sees fresh config.
func ClearRepoConfigCache(owner, repo string) {
	repoConfigCacheM.Lock()
	defer repoConfigCacheM.Unlock()
	if repoConfigCache == nil {
		return
	}
	prefix := owner + "\x00" + repo + "\x00"
	for k := range repoConfigCache {
		if strings.HasPrefix(k, prefix) {
			delete(repoConfigCache, k)
		}
	}
}

// Function getInstLoc gets the location of org-level configuration for this
// org/installation. The purpose is to only hit possible 404s once per run.
func getInstLoc(ctx context.Context, r repositories, owner string) (*instLoc, error) {
	mMutex.RLock()
	if instLocs == nil {
		mMutex.RUnlock()
		mMutex.Lock()
		instLocs = make(map[string]*instLoc)
		mMutex.Unlock()
	} else {
		mMutex.RUnlock()
	}
	mMutex.RLock()
	if il, ok := instLocs[owner]; ok {
		mMutex.RUnlock()
		return il, nil
	}
	mMutex.RUnlock()
	il, err := createIl(ctx, r, owner)
	if err != nil {
		return nil, err
	}
	mMutex.Lock()
	instLocs[owner] = il
	mMutex.Unlock()
	return il, nil
}

// Function ClearInstLoc clears any saved config locations for an org/installation.
func ClearInstLoc(owner string) {
	mMutex.RLock()
	if instLocs == nil {
		mMutex.RUnlock()
		return
	}
	if _, ok := instLocs[owner]; !ok {
		mMutex.RUnlock()
		return
	}
	mMutex.RUnlock()
	mMutex.Lock()
	delete(instLocs, owner)
	mMutex.Unlock()
}

func createIl(ctx context.Context, r repositories, owner string) (*instLoc, error) {
	_, rsp, err := r.Get(ctx, owner, operator.OrgConfigRepo)
	if err == nil {
		// ".allstar" repo exists
		return &instLoc{
			Exists: true,
			Repo:   operator.OrgConfigRepo,
			Path:   "",
		}, nil
	} else if rsp != nil && rsp.StatusCode == http.StatusNotFound {
		// ".allstar" repo does not exist
		_, rsp, err := r.Get(ctx, owner, githubConfRepo)
		if err == nil {
			// ".github" repo exists
			// ".github/allstar" may not exist but we will walk the path on any
			// getcontents to avoid a 404 for that
			return &instLoc{
				Exists: true,
				Repo:   githubConfRepo,
				Path:   operator.OrgConfigDir,
			}, nil
		} else if rsp != nil && rsp.StatusCode == http.StatusNotFound {
			// ".github" repo does not exist
			return &instLoc{
				Exists: false,
			}, nil
		} else {
			// Unknown error getting ".github" repo
			return nil, err
		}
	} else {
		// Unknown error getting ".allstar" repo
		return nil, err
	}
}
