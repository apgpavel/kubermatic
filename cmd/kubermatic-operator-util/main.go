/*
Copyright 2020 The Kubermatic Kubernetes Platform contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/urfave/cli"
	"go.uber.org/zap"

	"k8c.io/kubermatic/v2/pkg/controller/operator/common"
	operatorv1alpha1 "k8c.io/kubermatic/v2/pkg/crd/operator/v1alpha1"
	"k8c.io/kubermatic/v2/pkg/log"
	yamlutil "k8c.io/kubermatic/v2/pkg/util/yaml"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

var logger *zap.SugaredLogger

func main() {
	app := cli.NewApp()
	app.Name = "Kubermatic Operator Utility"
	app.Version = "v1.0.0"

	defaultLogFormat := log.FormatConsole

	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "log-debug",
			Usage: "Enables more verbose logging",
		},
		cli.GenericFlag{
			Name:  "log-format",
			Value: &defaultLogFormat,
			Usage: fmt.Sprintf("Use one of [%v] to change the log output", log.AvailableFormats),
		},
	}

	app.Commands = append([]cli.Command{
		{
			Name:      "defaults",
			Usage:     "Outputs a KubermaticConfiguration with all default values, optionally applied to a given configuration manifest (YAML)",
			Action:    defaultsAction,
			ArgsUsage: "[MANIFEST_FILE]",
		},
	}, extraCommands()...)

	// setup logging
	app.Before = func(c *cli.Context) error {
		format := c.GlobalGeneric("log-format").(*log.Format)
		rawLog := log.New(c.GlobalBool("log-debug"), *format)
		logger = rawLog.Sugar()

		return nil
	}

	err := app.Run(os.Args)
	// Only log failures when the logger has been setup, otherwise
	// we know it's been a CLI parsing failure and the cli package
	// has already output the error and printed the usage hints.
	if err != nil && logger != nil {
		logger.Fatalw("Failed to run command", zap.Error(err))
	}
}

func defaultsAction(ctx *cli.Context) error {
	config := &operatorv1alpha1.KubermaticConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "operator.kubermatic.io/v1alpha1",
			Kind:       "KubermaticConfiguration",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubermatic",
			Namespace: "kubermatic",
		},
	}

	configFile := ctx.Args().First()
	if configFile != "" {
		content, err := ioutil.ReadFile(configFile)
		if err != nil {
			return cli.NewExitError(fmt.Errorf("failed to read file %s: %v", configFile, err), 1)
		}

		if err := yaml.Unmarshal(content, &config); err != nil {
			return cli.NewExitError(fmt.Errorf("failed to parse file %s as YAML: %v", configFile, err), 1)
		}
	}

	logger := zap.NewNop().Sugar()
	defaulted, err := common.DefaultConfiguration(config, logger)
	if err != nil {
		return cli.NewExitError(fmt.Errorf("failed to create KubermaticConfiguration: %v", err), 1)
	}

	if err := yamlutil.Encode(defaulted, os.Stdout); err != nil {
		return cli.NewExitError(fmt.Errorf("failed to create YAML: %v", err), 1)
	}

	return nil
}
