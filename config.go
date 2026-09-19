package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config mirrors config.json, stored next to the executable.
type Config struct {
	// Username accepts a student id, a short name or a full mail address.
	Username string `json:"username"`
	Password string `json:"password"`
	// RememberPassword decides whether Password is written to disk.
	RememberPassword bool `json:"rememberPassword"`

	// PortalBaseURL is the portal home page, which embeds the client IP the
	// portal sees (the NAT egress address, not the local one).
	PortalBaseURL string `json:"portalBaseUrl"`
	// EportalURL is the Dr.COM eportal root, note the port 801.
	EportalURL string `json:"eportalUrl"`

	// AccountPrefix is fixed to ",0," for a desktop browser login.
	AccountPrefix string `json:"accountPrefix"`
	// AccountSuffix selects the ISP, see Operators.
	AccountSuffix string `json:"accountSuffix"`

	CheckIntervalSeconds int      `json:"checkIntervalSeconds"`
	HTTPTimeoutSeconds   int      `json:"httpTimeoutSeconds"`
	AutoLogin            bool     `json:"autoLogin"`
	CheckInternet        bool     `json:"checkInternet"`
	ProbeURLs            []string `json:"probeUrls"`
	JsVersion            string   `json:"jsVersion"`

	path string
}

func defaultConfig() *Config {
	return &Config{
		PortalBaseURL:        "http://10.9.1.3/",
		EportalURL:           "http://10.9.1.3:801/eportal/",
		AccountPrefix:        ",0,",
		CheckIntervalSeconds: 15,
		HTTPTimeoutSeconds:   6,
		AutoLogin:            true,
		RememberPassword:     true,
		CheckInternet:        true,
		ProbeURLs: []string{
			"http://connect.rom.miui.com/generate_204",
			"http://www.baidu.com",
			"http://www.msftconnecttest.com/connecttest.txt",
		},
		JsVersion: "3.3.3",
	}
}

// LoadConfig reads path, creating it with defaults when missing.
func LoadConfig(path string) (*Config, error) {
	cfg := defaultConfig()
	cfg.path = path

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if e := cfg.Save(); e != nil {
				return cfg, fmt.Errorf("write default config: %w", e)
			}
			return cfg, nil
		}
		return cfg, err
	}
	// Unmarshal onto the defaults so fields added later keep sane values.
	if err := json.Unmarshal(data, cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.normalize()
	return cfg, nil
}

func (c *Config) Path() string { return c.path }

func (c *Config) normalize() {
	if c.CheckIntervalSeconds < 5 {
		c.CheckIntervalSeconds = 5
	}
	if c.HTTPTimeoutSeconds < 2 {
		c.HTTPTimeoutSeconds = 2
	}
	if strings.TrimSpace(c.PortalBaseURL) == "" {
		c.PortalBaseURL = defaultConfig().PortalBaseURL
	}
	if strings.TrimSpace(c.EportalURL) == "" {
		c.EportalURL = defaultConfig().EportalURL
	}
	if strings.TrimSpace(c.JsVersion) == "" {
		c.JsVersion = defaultConfig().JsVersion
	}
}

// Save writes config.json, omitting the password when it is not remembered.
func (c *Config) Save() error {
	c.normalize()
	data, err := c.marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, append(data, '\n'), 0o644)
}

func (c *Config) marshal() ([]byte, error) {
	type persistConfig Config
	v := persistConfig(*c)
	if !c.RememberPassword {
		v.Password = ""
	}
	return json.MarshalIndent(v, "", "  ")
}

// Operator is one ISP entry of the login page dropdown.
type Operator struct {
	Name   string
	Suffix string
}

// Operators mirrors the ISP_select options the portal login page serves.
var Operators = []Operator{
	{"Campus network", ""},
	{"China Mobile", "@zgyd"},
	{"China Unicom", "@cucc"},
	{"China Telecom", "@ctc"},
}

func (c *Config) OperatorName() string {
	for _, op := range Operators {
		if op.Suffix == c.AccountSuffix {
			return op.Name
		}
	}
	return "custom(" + c.AccountSuffix + ")"
}

func (c *Config) Configured() bool {
	return strings.TrimSpace(c.Username) != "" && c.Password != ""
}

// Account is the value submitted as user_account.
func (c *Config) Account() string {
	return c.AccountPrefix + c.Username + c.AccountSuffix
}
