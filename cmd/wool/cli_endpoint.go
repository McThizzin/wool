package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"wool/internal/daemon"
	"wool/internal/endpoint"
	"wool/internal/llamaserver"
)

const endpointUsage = `usage: wool endpoint [server|router|MODEL] [options]

Shows the base URL, model ID and copy-paste snippets for a running instance.
Without a target the standalone server wins, then the router. MODEL must be
listed by the running router.

options:
  --json           print the stable machine-readable result`

type endpointOptions struct {
	target string
	asJSON bool
}

func parseEndpointArgs(args []string) (endpointOptions, error) {
	var o endpointOptions
	for _, a := range args {
		switch {
		case a == "--json":
			o.asJSON = true
		case a == "-h" || a == "--help":
			return o, flag.ErrHelp
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown option %q", a)
		default:
			if o.target != "" {
				return o, fmt.Errorf("only one target is allowed")
			}
			o.target = a
		}
	}
	return o, nil
}

func cmdEndpoint(cfgPath string, args []string) int {
	opts, err := parseEndpointArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(endpointUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n%s\n", err, endpointUsage)
		return 2
	}
	ep, err := resolveEndpoint(cfgPath, opts.target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: endpoint: %v\n", err)
		return 1
	}
	if opts.asJSON {
		return writeJSON(ep)
	}
	printEndpoint(ep)
	return 0
}

// resolveEndpoint selects the running instance and model for the helper.
func resolveEndpoint(cfgPath, what string) (endpoint.Endpoint, error) {
	mgr := daemon.NewManager()
	switch what {
	case "server":
		inst, err := mgr.Status("server")
		if err != nil || !inst.Alive {
			return endpoint.Endpoint{}, errors.New("server is not running (start it with `wool run TARGET`)")
		}
		return endpoint.Build("server", inst.URL(), serverEndpointModel(cfgPath, inst)), nil
	case "router":
		inst, err := mgr.Status("router")
		if err != nil || !inst.Alive {
			return endpoint.Endpoint{}, errors.New("router is not running (start it with `wool router start`)")
		}
		model, err := endpointRouterModel(inst, "")
		if err != nil {
			return endpoint.Endpoint{}, err
		}
		return endpoint.Build("router", inst.URL(), model), nil
	case "":
		if inst, err := mgr.Status("server"); err == nil && inst.Alive {
			return endpoint.Build("server", inst.URL(), serverEndpointModel(cfgPath, inst)), nil
		}
		if inst, err := mgr.Status("router"); err == nil && inst.Alive {
			model, err := endpointRouterModel(inst, "")
			if err != nil {
				return endpoint.Endpoint{}, err
			}
			return endpoint.Build("router", inst.URL(), model), nil
		}
		return endpoint.Endpoint{}, errors.New("no server is running (start one with `wool run TARGET` or `wool router start`)")
	default:
		inst, err := mgr.Status("router")
		if err != nil || !inst.Alive {
			return endpoint.Endpoint{}, errors.New("router is not running (start it with `wool router start`)")
		}
		model, err := endpointRouterModel(inst, what)
		if err != nil {
			return endpoint.Endpoint{}, err
		}
		return endpoint.Build("router", inst.URL(), model), nil
	}
}

// serverEndpointModel prefers the model ID the running server actually
// reports. Older or unreachable servers fall back to the recorded config
// alias, then the recorded model ID.
func serverEndpointModel(cfgPath string, inst daemon.Instance) string {
	client := llamaserver.NewClient(inst.Host, inst.Port)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if ids, err := client.OpenAIModels(ctx); err == nil && len(ids) > 0 {
		sort.Strings(ids)
		return ids[0]
	}
	if cfg, err := loadConfig(cfgPath); err == nil && inst.Model != "" {
		if r, err := cfg.Resolve(inst.Model, nil); err == nil && r.Alias != "" {
			return r.Alias
		}
	}
	return inst.Model
}

// endpointRouterModel picks a router model. With want empty it prefers a
// resident model, then the first listed model, both sorted for determinism.
func endpointRouterModel(inst daemon.Instance, want string) (string, error) {
	client := llamaserver.NewClient(inst.Host, inst.Port)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	models, err := client.Models(ctx, false)
	if err != nil {
		return "", fmt.Errorf("router models: %w", err)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	if want != "" {
		for _, rm := range models {
			if rm.ID == want {
				return rm.ID, nil
			}
		}
		return "", fmt.Errorf("router does not list %s", want)
	}
	for _, rm := range models {
		if rm.Status.Value == "loaded" || rm.Status.Value == "sleeping" {
			return rm.ID, nil
		}
	}
	if len(models) > 0 {
		return models[0].ID, nil
	}
	return "", errors.New("router is running but lists no models")
}

func printEndpoint(ep endpoint.Endpoint) {
	fmt.Printf("%s · %s\n", ep.Source, ep.BaseURL)
	fmt.Printf("  base URL  %s\n", ep.BaseURL)
	fmt.Printf("  API base  %s\n", ep.APIBase)
	fmt.Printf("  model     %s\n", ep.Model)
	for _, s := range ep.Snippets {
		fmt.Printf("\n%s\n", s.Name)
		for _, line := range strings.Split(s.Text, "\n") {
			fmt.Printf("  %s\n", line)
		}
	}
}

// logsSnapshot is the stable --json shape for parsed request rows.
