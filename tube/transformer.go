/*
 * Copyright 2018-2020 the original author or authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package tube

import (
	"context"
	"fmt"
	"log"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/google/go-github/v29/github"
	"gopkg.in/yaml.v3"
)

type Transformer struct {
	DescriptorPath string
	PipelinePath   string

	GitHubUsername    string
	GitHubAccessToken string
	WebHookSalt       string

	ConcourseURI  string
	ConcourseTeam string
}

type PipelineContributor interface {
	Group() string
	Job() Job
	Resources() []Resource
}

func (t *Transformer) Transform() error {
	var err error

	d, err := NewDescriptor(t.DescriptorPath)
	if err != nil {
		return fmt.Errorf("unable to read descriptor: %w", err)
	}

	log.Println(d.Name)

	contributors := []PipelineContributor{
		ReleaseMajorContributor{Descriptor: d, Salt: t.WebHookSalt},
		ReleaseMinorContributor{Descriptor: d, Salt: t.WebHookSalt},
		ReleasePatchContributor{Descriptor: d, Salt: t.WebHookSalt},
		TestContributor{Descriptor: d, Salt: t.WebHookSalt},
	}

	if m, err := NewModuleDependenciesContributor(d, t.WebHookSalt); err != nil {
		return fmt.Errorf("unable to create new module dependencies job: %w", err)
	} else {
		contributors = append(contributors, m)
	}

	if !reflect.DeepEqual(Package{}, d.Package) {
		contributors = append(contributors, PackageContributor{Descriptor: d, Salt: t.WebHookSalt})
	}

	for _, dep := range d.Dependencies {
		contributors = append(contributors, DependencyContributor{Descriptor: d, Dependency: dep, Salt: t.WebHookSalt})
	}

	p := NewPipeline(d.ShortName())
	for _, c := range contributors {
		j := c.Job()
		r := c.Resources()

		log.Printf("  %s\n", j.Name)

		for _, r := range r {
			t, ok := KnownResourceTypes[r.Type]
			if !ok {
				log.Fatalf("unable to find resource type %s", r.Type)
			}

			p.ResourceTypes.Add(t)
			p.Resources.Add(r)
		}

		p.Groups.Add(c.Group(), j.Name)
		p.Jobs.Add(j)

	}

	if err := t.WritePipeline(p); err != nil {
		return fmt.Errorf("unable to write pipeline: %w", err)
	}

	if err := t.CreateWebHooks(p); err != nil {
		return fmt.Errorf("unable to create webhooks: %w", err)
	}

	return nil
}

func (t *Transformer) CreateWebHooks(pipeline Pipeline) error {
	var r []Resource
	for _, v := range pipeline.Resources {
		if !reflect.DeepEqual(WebHook{}, v.WebHook) {
			r = append(r, v)
		}
	}

	sort.Slice(r, func(i, j int) bool {
		return fmt.Sprintf("%s%s", r[i].WebHook.Owner, r[i].WebHook.Repository) < r[j].Name
	})

	gh := github.NewClient((&github.BasicAuthTransport{
		Username: t.GitHubUsername,
		Password: t.GitHubAccessToken,
	}).Client())

	for _, r := range r {
		if err := t.CreateWebHook(pipeline, r, gh); err != nil {
			return fmt.Errorf("unable to create webhook: %w", err)
		}
	}

	return nil
}

func (t *Transformer) CreateWebHook(pipeline Pipeline, resource Resource, gh *github.Client) error {
	w := resource.WebHook

	uri := fmt.Sprintf("%s/api/v1/teams/%s/pipelines/%s/resources/%s/check/webhook",
		t.ConcourseURI, t.ConcourseTeam, pipeline.Name, resource.Name)

	var hooks []*github.Hook
	opt := &github.ListOptions{PerPage: 100}
	for {
		h, r, err := gh.Repositories.ListHooks(context.Background(), w.Owner, w.Repository, opt)
		if err != nil {
			return fmt.Errorf("unable to list existing webhooks for %s/%s: %w", w.Owner, w.Repository, err)
		}

		hooks = append(hooks, h...)

		if r.NextPage == 0 {
			break
		}
		opt.Page = r.NextPage
	}

	var existing *github.Hook
	for _, h := range hooks {
		if strings.HasPrefix(h.Config["url"].(string), uri) {
			existing = h
			break
		}
	}

	if existing == nil {
		log.Printf("  create webhook: %s/%s ➜ %s/%s\n", w.Owner, w.Repository, pipeline.Name, resource.Name)

		hook := &github.Hook{
			Config: map[string]interface{}{
				"url": fmt.Sprintf("%s?webhook_token=%s", uri, w.Token),
			},
		}

		if _, _, err := gh.Repositories.CreateHook(context.Background(), w.Owner, w.Repository, hook); err != nil {
			return fmt.Errorf("unable to create webhook %s/%s: %w", w.Owner, w.Repository, err)
		}
	} else if existing.Config["url"].(string) != fmt.Sprintf("%s?webhook_token=%s", uri, w.Token) {
		log.Printf("  update webhook: %s/%s ➜ %s/%s\n", w.Owner, w.Repository, pipeline.Name, resource.Name)

		hook := &github.Hook{
			Config: map[string]interface{}{
				"url": fmt.Sprintf("%s?webhook_token=%s", uri, w.Token),
			},
		}

		if _, _, err := gh.Repositories.EditHook(context.Background(), w.Owner, w.Repository, existing.GetID(), hook); err != nil {
			return fmt.Errorf("unable to update webhook %s/%s: %w", w.Owner, w.Repository, err)
		}
	} else {
		log.Printf("  existing webhook: %s/%s ➜ %s/%s\n", w.Owner, w.Repository, pipeline.Name, resource.Name)
	}

	return nil
}

func (t *Transformer) WritePipeline(pipeline Pipeline) error {
	out := os.Stdout
	if t.PipelinePath != "" {
		out, err := os.OpenFile(t.PipelinePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("unable to open %s: %w", t.PipelinePath, err)
		}
		defer out.Close()
	}

	if err := yaml.NewEncoder(out).Encode(pipeline); err != nil {
		return fmt.Errorf("unable to encode pipeline: %w", err)
	}

	return nil
}
