package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Global persistent flag values.
var (
	namespace   string
	release     string
	kubecfgPath string
	kubeCtx     string
)

func main() {
	root := &cobra.Command{
		Use:   "orbctl",
		Short: "Manage orbit scenarios and annotate Grafana dashboards",
	}

	root.PersistentFlags().StringVarP(&namespace, "namespace", "n", "default", "Kubernetes namespace")
	root.PersistentFlags().StringVar(&release, "release", "orbit", "Helm release name (ConfigMap = <release>-scenarios)")
	root.PersistentFlags().StringVar(&kubecfgPath, "kubeconfig", "", "path to kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")
	root.PersistentFlags().StringVar(&kubeCtx, "context", "", "kubeconfig context to use")

	root.AddCommand(newSetCmd(), newListCmd(), newGetCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newSetCmd() *cobra.Command {
	var (
		valuesPath   string
		grafanaURL   string
		grafanaToken string
		dashboards   []string
	)

	cmd := &cobra.Command{
		Use:   "set <scenario>",
		Short: "Set the active orbit scenario",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scenarioName := args[0]

			// Pre-flight: validate against local values file before touching the cluster.
			if valuesPath != "" {
				if err := validateValuesFile(valuesPath, scenarioName); err != nil {
					return err
				}
				slog.Info("scenario found in values file", "scenario", scenarioName)
			}

			client, err := buildClient(kubecfgPath, kubeCtx)
			if err != nil {
				return fmt.Errorf("build k8s client: %w", err)
			}

			cmName := release + "-scenarios"
			ctx := context.Background()

			cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, cmName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("get configmap %s/%s: %w", namespace, cmName, err)
			}

			raw, ok := cm.Data["scenarios.yaml"]
			if !ok {
				return fmt.Errorf("configmap %s has no scenarios.yaml key", cmName)
			}

			// Parse as a generic map to preserve all fields on round-trip.
			var data map[string]interface{}
			if err := yaml.Unmarshal([]byte(raw), &data); err != nil {
				return fmt.Errorf("parse scenarios.yaml: %w", err)
			}

			if err := assertScenarioExists(data, scenarioName); err != nil {
				return err
			}

			// Capture current for the transitional annotation text.
			prevScenario := activeScenario(data)
			if prevScenario == "" {
				prevScenario = "(none)"
			}

			data["activeScenario"] = scenarioName

			updated, err := yaml.Marshal(data)
			if err != nil {
				return fmt.Errorf("marshal updated config: %w", err)
			}

			cm.Data["scenarios.yaml"] = string(updated)
			if _, err := client.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
				return fmt.Errorf("update configmap: %w", err)
			}

			fmt.Printf("active scenario: %s → %s\n", prevScenario, scenarioName)

			// Grafana transitional annotation.
			if grafanaURL != "" && grafanaToken != "" {
				text := fmt.Sprintf("orbit scenario: %s → %s", prevScenario, scenarioName)
				tags := []string{"orbit", scenarioName}
				nowMs := time.Now().UnixMilli()

				if len(dashboards) == 0 {
					if err := postAnnotation(grafanaURL, grafanaToken, "", text, tags, nowMs); err != nil {
						slog.Warn("grafana annotation failed", "error", err)
					} else {
						fmt.Println("grafana annotation posted (global)")
					}
				} else {
					for _, uid := range dashboards {
						if err := postAnnotation(grafanaURL, grafanaToken, uid, text, tags, nowMs); err != nil {
							slog.Warn("grafana annotation failed", "dashboard", uid, "error", err)
						} else {
							fmt.Printf("grafana annotation posted: dashboard=%s\n", uid)
						}
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&valuesPath, "values", "", "path to Helm values.yaml for pre-flight scenario validation")
	cmd.Flags().StringVar(&grafanaURL, "grafana-url", "", "Grafana base URL (e.g. http://grafana:3000)")
	cmd.Flags().StringVar(&grafanaToken, "token", "", "Grafana API Bearer token")
	cmd.Flags().StringArrayVar(&dashboards, "dashboard", nil, "Grafana dashboard UID to annotate (repeatable)")

	return cmd
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available scenarios and the current active scenario",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := fetchConfigMapData()
			if err != nil {
				return err
			}

			names := scenarioNames(data)
			sort.Strings(names)

			active := activeScenario(data)
			display := active
			if display == "" {
				display = "(none)"
			}

			fmt.Printf("active: %s\n\nscenarios:\n", display)
			for _, n := range names {
				marker := "  "
				if n == active {
					marker = "* "
				}
				fmt.Printf("%s%s\n", marker, n)
			}
			return nil
		},
	}
}

func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Print the current active scenario",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := fetchConfigMapData()
			if err != nil {
				return err
			}
			active := activeScenario(data)
			if active == "" {
				fmt.Println("(none)")
			} else {
				fmt.Println(active)
			}
			return nil
		},
	}
}

// fetchConfigMapData fetches the scenarios ConfigMap and returns the parsed YAML data map.
func fetchConfigMapData() (map[string]interface{}, error) {
	client, err := buildClient(kubecfgPath, kubeCtx)
	if err != nil {
		return nil, fmt.Errorf("build k8s client: %w", err)
	}

	cmName := release + "-scenarios"
	cm, err := client.CoreV1().ConfigMaps(namespace).Get(context.Background(), cmName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get configmap %s/%s: %w", namespace, cmName, err)
	}

	raw, ok := cm.Data["scenarios.yaml"]
	if !ok {
		return nil, fmt.Errorf("configmap %s has no scenarios.yaml key", cmName)
	}

	var data map[string]interface{}
	if err := yaml.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("parse scenarios.yaml: %w", err)
	}
	return data, nil
}

// activeScenario extracts the activeScenario string from the data map.
func activeScenario(data map[string]interface{}) string {
	v, _ := data["activeScenario"].(string)
	return v
}

// scenarioNames returns the keys of the scenarios sub-map.
func scenarioNames(data map[string]interface{}) []string {
	scenarios, ok := data["scenarios"].(map[string]interface{})
	if !ok {
		return nil
	}
	names := make([]string, 0, len(scenarios))
	for k := range scenarios {
		names = append(names, k)
	}
	return names
}

// assertScenarioExists returns an error listing available scenarios if name is not found.
func assertScenarioExists(data map[string]interface{}, name string) error {
	scenarios, ok := data["scenarios"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("no scenarios defined in ConfigMap")
	}
	if _, exists := scenarios[name]; exists {
		return nil
	}
	names := make([]string, 0, len(scenarios))
	for k := range scenarios {
		names = append(names, k)
	}
	sort.Strings(names)
	return fmt.Errorf("scenario %q not found; available: %s", name, strings.Join(names, ", "))
}

// validateValuesFile parses a Helm values.yaml and verifies the named scenario is defined.
func validateValuesFile(path, scenario string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read values file: %w", err)
	}

	var vf struct {
		Scenarios map[string]interface{} `yaml:"scenarios"`
	}
	if err := yaml.Unmarshal(raw, &vf); err != nil {
		return fmt.Errorf("parse values file: %w", err)
	}

	if _, ok := vf.Scenarios[scenario]; ok {
		return nil
	}

	names := make([]string, 0, len(vf.Scenarios))
	for k := range vf.Scenarios {
		names = append(names, k)
	}
	sort.Strings(names)
	return fmt.Errorf("scenario %q not in values file; available: %s", scenario, strings.Join(names, ", "))
}

// buildClient returns a Kubernetes clientset using in-cluster config or kubeconfig.
func buildClient(kubeconfigPath, contextName string) (*kubernetes.Clientset, error) {
	// Prefer in-cluster when no explicit override is given.
	if kubeconfigPath == "" && contextName == "" {
		if cfg, err := rest.InClusterConfig(); err == nil {
			return kubernetes.NewForConfig(cfg)
		}
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}

	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restCfg)
}

// postAnnotation posts a Grafana annotation. dashboardUID may be empty for a global annotation.
func postAnnotation(baseURL, token, dashboardUID, text string, tags []string, timeMs int64) error {
	body := map[string]interface{}{
		"time": timeMs,
		"tags": tags,
		"text": text,
	}
	if dashboardUID != "" {
		body["dashboardUID"] = dashboardUID
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/api/annotations",
		bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("grafana returned %s", resp.Status)
	}
	return nil
}
