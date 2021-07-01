package loader

import (
	"bytes"
	"errors"
	"io/ioutil"
	"os"
	"strings"

	"github.com/projectdiscovery/gologger"
	"github.com/projectdiscovery/nuclei/v2/pkg/catalog"
	"github.com/projectdiscovery/nuclei/v2/pkg/protocols"
	"github.com/projectdiscovery/nuclei/v2/pkg/templates"
	"github.com/projectdiscovery/nuclei/v2/pkg/types"
	"gopkg.in/yaml.v2"
)

// Config contains the configuration options for the loader
type Config struct {
	Templates        []string
	Workflows        []string
	ExcludeTemplates []string
	IncludeTemplates []string

	Tags        []string
	ExcludeTags []string
	Authors     []string
	Severities  []string
	IncludeTags []string

	Catalog            *catalog.Catalog
	ExecutorOptions    protocols.ExecuterOptions
	TemplatesDirectory string
}

// Store is a storage for loaded nuclei templates
type Store struct {
	tagFilter      *tagFilter
	config         *Config
	finalTemplates []string

	templates []*templates.Template
	workflows []*templates.Template
}

// New creates a new template store based on provided configuration
func New(config *Config) (*Store, error) {
	// Create a tag filter based on provided configuration
	store := &Store{
		config:    config,
		tagFilter: config.createTagFilter(),
	}

	// Handle a case with no templates or workflows, where we use base directory
	if len(config.Templates) == 0 && len(config.Workflows) == 0 {
		config.Templates = append(config.Templates, config.TemplatesDirectory)
	}
	store.finalTemplates = append(store.finalTemplates, config.Templates...)

	return store, nil
}

// Templates returns all the templates in the store
func (s *Store) Templates() []*templates.Template {
	return s.templates
}

// Workflows returns all the workflows in the store
func (s *Store) Workflows() []*templates.Template {
	return s.workflows
}

// Load loads all the templates from a store, performs filtering and returns
// the complete compiled templates for a nuclei execution configuration.
func (s *Store) Load() {
	includedTemplates := s.config.Catalog.GetTemplatesPath(s.finalTemplates)
	includedWorkflows := s.config.Catalog.GetTemplatesPath(s.config.Workflows)
	excludedTemplates := s.config.Catalog.GetTemplatesPath(s.config.ExcludeTemplates)
	alwaysIncludeTemplates := s.config.Catalog.GetTemplatesPath(s.config.IncludeTemplates)

	alwaysIncludedTemplatesMap := make(map[string]struct{})
	for _, tpl := range alwaysIncludeTemplates {
		alwaysIncludedTemplatesMap[tpl] = struct{}{}
	}

	templatesMap := make(map[string]struct{})
	for _, tpl := range includedTemplates {
		templatesMap[tpl] = struct{}{}
	}
	for _, template := range excludedTemplates {
		if _, ok := alwaysIncludedTemplatesMap[template]; ok {
			continue
		} else {
			delete(templatesMap, template)
		}
	}

	for k := range templatesMap {
		loaded, err := s.loadTemplateParseMetadata(k, false)
		if err != nil {
			gologger.Warning().Msgf("Could not load template %s: %s\n", k, err)
		}
		if loaded {
			parsed, err := templates.Parse(k, s.config.ExecutorOptions)
			if err != nil {
				gologger.Warning().Msgf("Could not parse template %s: %s\n", k, err)
			} else if parsed != nil {
				s.templates = append(s.templates, parsed)
			}
		}
	}

	workflowsMap := make(map[string]struct{})
	for _, tpl := range includedWorkflows {
		workflowsMap[tpl] = struct{}{}
	}
	for _, template := range excludedTemplates {
		if _, ok := alwaysIncludedTemplatesMap[template]; ok {
			continue
		} else {
			delete(templatesMap, template)
		}
	}
	for k := range workflowsMap {
		loaded, err := s.loadTemplateParseMetadata(k, true)
		if err != nil {
			gologger.Warning().Msgf("Could not load workflow %s: %s\n", k, err)
		}

		if loaded {
			parsed, err := templates.Parse(k, s.config.ExecutorOptions)
			if err != nil {
				gologger.Warning().Msgf("Could not parse workflow %s: %s\n", k, err)
			} else if parsed != nil {
				s.workflows = append(s.workflows, parsed)
			}
		}
	}
}

// loadTemplateParseMetadata loads a template by parsing metadata and running
// all tag and path based filters on the template.
func (s *Store) loadTemplateParseMetadata(templatePath string, workflow bool) (bool, error) {
	f, err := os.Open(templatePath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	data, err := ioutil.ReadAll(f)
	if err != nil {
		return false, err
	}

	template := &templates.Template{}
	err = yaml.NewDecoder(bytes.NewReader(data)).Decode(template)
	if err != nil {
		return false, err
	}
	if _, ok := template.Info["name"]; !ok {
		return false, errors.New("no template name field provided")
	}
	author, ok := template.Info["author"]
	if !ok {
		return false, errors.New("no template author field provided")
	}
	severity, ok := template.Info["severity"]
	if !ok {
		severity = ""
	}

	templateTags, ok := template.Info["tags"]
	if !ok {
		templateTags = ""
	}
	tagStr := types.ToString(templateTags)

	tags := strings.Split(tagStr, ",")
	severityStr := types.ToString(severity)
	authors := strings.Split(types.ToString(author), ",")

	matched := false

	for _, tag := range tags {
		for _, author := range authors {
			match, err := s.tagFilter.match(strings.TrimSpace(tag), strings.TrimSpace(author), severityStr)
			if err == ErrExcluded {
				return false, ErrExcluded
			}
			if !matched && match && err == nil {
				matched = true
			}
		}
	}
	if !matched {
		return false, nil
	}
	if len(template.Workflows) == 0 && workflow {
		return false, nil
	}
	if len(template.Workflows) > 0 && !workflow {
		return false, nil
	}
	return true, nil
}