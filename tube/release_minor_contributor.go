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

type ReleaseMinorContributor struct {
	Descriptor Descriptor
	Salt       string
}

func (ReleaseMinorContributor) Group() string {
	return "build"
}

func (r ReleaseMinorContributor) Job() Job {
	s := NewSourceResource(r.Descriptor, r.Salt)
	v := NewVersionResource(r.Descriptor)

	return Job{
		Name:   "release-minor",
		Public: true,
		Plan: []map[string]interface{}{
			{
				"in_parallel": []map[string]interface{}{
					{
						"get":      "source",
						"resource": s.Name,
						"passed":   []string{"test"},
					},
					{
						"get":      "version",
						"resource": v.Name,
						"params": map[string]interface{}{
							"bump": "minor",
						},
					},
				},
			},
			{
				"in_parallel": []map[string]interface{}{
					{
						"put": s.Name,
						"params": map[string]interface{}{
							"repository": "source",
							"only_tag":   true,
							"tag":        "version/version",
							"tag_prefix": "v",
						},
					},
					{
						"put": v.Name,
						"params": map[string]interface{}{
							"bump": "minor",
						},
					},
				},
			},
		},
	}
}

func (r ReleaseMinorContributor) Resources() []Resource {
	return []Resource{
		NewSourceResource(r.Descriptor, r.Salt),
		NewVersionResource(r.Descriptor),
	}
}

