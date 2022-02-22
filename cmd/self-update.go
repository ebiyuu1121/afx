package cmd

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/b4b4r07/afx/pkg/config"
	"github.com/b4b4r07/afx/pkg/errors"
	"github.com/b4b4r07/afx/pkg/templates"
	"github.com/creativeprojects/go-selfupdate"
	"github.com/fatih/color"
	"github.com/inconshreveable/go-update"
	"github.com/spf13/cobra"
	"github.com/tidwall/gjson"
)

type selfUpdateCmd struct {
	meta

	opt selfUpdateOpt

	annotation map[string]string
}

type selfUpdateOpt struct {
	tag bool
}

var (
	// selfUpdateLong is long description of self-update command
	selfUpdateLong = templates.LongDesc(``)

	// selfUpdateExample is examples for selfUpdate command
	selfUpdateExample = templates.Examples(`
		afx self-update
	`)
)

// newSelfUpdateCmd creates a new selfUpdate command
func newSelfUpdateCmd() *cobra.Command {
	info := color.New(color.FgGreen).SprintFunc()
	c := &selfUpdateCmd{
		annotation: map[string]string{
			"0.1.11": info(`Run "afx state refresh --force" at first!`),
		},
	}

	selfUpdateCmd := &cobra.Command{
		Use:                   "self-update",
		Short:                 "Update afx itself to latest version",
		Long:                  selfUpdateLong,
		Example:               selfUpdateExample,
		DisableFlagsInUseLine: true,
		SilenceUsage:          true,
		SilenceErrors:         true,
		Args:                  cobra.MaximumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.meta.init(args); err != nil {
				return err
			}

			if c.opt.tag {
				return c.selectTag(args)
			}

			return c.run(args)
		},
	}

	flag := selfUpdateCmd.Flags()
	flag.BoolVarP(&c.opt.tag, "select", "", false, "help message")
	flag.MarkHidden("select")

	return selfUpdateCmd
}

func (c *selfUpdateCmd) selectTag(args []string) error {
	resp, err := http.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases", Repository))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	var tags []string
	gjson.Get(string(body), "#.tag_name").
		ForEach(func(key, value gjson.Result) bool {
			tags = append(tags, value.String())
			return true
		})

	var tag string
	prompt := &survey.Select{
		Message: "Choose a tag you upgrade/downgrade:",
		Options: tags,
	}
	survey.AskOne(prompt, &tag, survey.WithValidator(survey.Required))

	release := config.GitHubRelease{
		Name:     "afx",
		Client:   http.DefaultClient,
		Assets:   config.Assets{},
		Filename: "",
	}

	rel := gjson.Get(string(body), fmt.Sprintf("#(tag_name==\"%s\")", tag))
	assets := rel.Get("assets")
	assets.ForEach(func(key, value gjson.Result) bool {
		name := value.Get("name").String()
		release.Assets = append(release.Assets, config.Asset{
			Name: name,
			Home: filepath.Join(os.Getenv("HOME"), ".afx"),
			Path: filepath.Join(os.Getenv("HOME"), ".afx", name),
			URL:  value.Get("browser_download_url").String(),
		})
		return true
	})

	ctx := context.Background()
	asset, err := release.Download(ctx)
	if err != nil {
		return err
	}

	if err := release.Unarchive(asset); err != nil {
		return err
	}

	fp, err := os.Open(filepath.Join(asset.Home, "afx"))
	if err != nil {
		return err
	}
	defer fp.Close()

	exe, err := os.Executable()
	if err != nil {
		return errors.New("could not locate executable path")
	}

	return update.Apply(fp, update.Options{
		TargetPath: exe,
	})
}

func (c *selfUpdateCmd) run(args []string) error {
	switch Version {
	case "unset":
		fmt.Fprintf(os.Stderr, "%s\n\n  %s\n  %s\n\n",
			color.RedString("Failed to update to new version!"),
			"You need to get precompiled version from GitHub releases.",
			fmt.Sprintf("This version (%s/%s) is compiled from source code.",
				Version, runtime.Version()),
		)
		return errors.New("failed to run self-update")
	}

	latest, found, err := selfupdate.DetectLatest(Repository)
	if err != nil {
		return errors.Wrap(err, "error occurred while detecting version")
	}

	if !found {
		return fmt.Errorf("latest version for %s/%s could not be found from GitHub repository",
			runtime.GOOS, runtime.GOARCH)
	}

	if latest.LessOrEqual(Version) {
		fmt.Printf("Current version (%s) is the latest\n", Version)
		return nil
	}

	yes := false
	if err := survey.AskOne(&survey.Confirm{
		Message: fmt.Sprintf("Do you want to update to %s? (current version: %s)",
			latest.Version(), Version),
	}, &yes); err != nil {
		return errors.Wrap(err, "cannot get answer from console")
	}
	if !yes {
		// do nothing
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return errors.New("could not locate executable path")
	}

	if err := selfupdate.UpdateTo(latest.AssetURL, latest.AssetName, exe); err != nil {
		return errors.Wrap(err, "error occurred while updating binary")
	}

	color.New(color.Bold).Printf("Successfully updated to version %s\n", latest.Version())

	var vs []*semver.Version
	for v := range c.annotation {
		vs = append(vs, semver.MustParse(v))
	}
	sort.Sort(semver.Collection(vs))

	var messages []string
	for _, v := range vs {
		start := semver.MustParse(Version)
		stop := semver.MustParse(latest.Version())

		log.Printf("[DEBUG] (self-update) Current version: %s", start)
		log.Printf("[DEBUG] (self-update) Next version:    %s", v)

		if stop.LessThan(v) {
			break
		}

		if v.GreaterThan(start) {
			messages = append(messages, "- "+c.annotation[v.String()])
		}
	}

	if len(messages) > 0 {
		fmt.Printf("\nTo use %q version:\n%s\n",
			latest.Version(),
			strings.Join(messages, "\n"))
	}

	return nil
}
