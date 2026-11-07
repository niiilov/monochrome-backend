package live

import (
	"bufio"
	"net/url"
	"strings"
)

type robotRule struct {
	allow bool
	path  string
}

type robotGroup struct {
	agents []string
	rules  []robotRule
}

// robotsAllow reports whether path may be fetched for this user agent.
// An empty body allows everything. The longest matching rule wins, and Allow beats Disallow on a tie.
func robotsAllow(body []byte, ua, path string) bool {
	groups := parseRobots(string(body))
	rules := matchGroup(groups, ua)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	bestLen := -1
	bestAllow := true
	for _, rule := range rules {
		if rule.path == "" {
			continue
		}
		if path == rule.path || strings.HasPrefix(path, rule.path) {
			if len(rule.path) > bestLen || (len(rule.path) == bestLen && rule.allow && !bestAllow) {
				bestLen = len(rule.path)
				bestAllow = rule.allow
			}
		}
	}
	return bestAllow
}

func parseRobots(text string) []robotGroup {
	var groups []robotGroup
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		switch key {
		case "user-agent":
			if len(groups) == 0 || len(groups[len(groups)-1].rules) > 0 {
				groups = append(groups, robotGroup{})
			}
			if val != "" {
				groups[len(groups)-1].agents = append(groups[len(groups)-1].agents, val)
			}
		case "allow", "disallow":
			if len(groups) == 0 {
				groups = append(groups, robotGroup{agents: []string{"*"}})
			}
			groups[len(groups)-1].rules = append(groups[len(groups)-1].rules, robotRule{
				allow: key == "allow",
				path:  val,
			})
		}
	}
	return groups
}

func matchGroup(groups []robotGroup, ua string) []robotRule {
	ua = strings.ToLower(strings.TrimSpace(ua))
	bestLen := -1
	best := -1
	star := -1
	for i, g := range groups {
		for _, agent := range g.agents {
			name := strings.ToLower(strings.TrimSpace(agent))
			if name == "*" {
				if star < 0 {
					star = i
				}
				continue
			}
			if ua == name || strings.HasPrefix(ua, name) {
				if len(name) > bestLen {
					bestLen = len(name)
					best = i
				}
			}
		}
	}
	if best >= 0 {
		return groups[best].rules
	}
	if star >= 0 {
		return groups[star].rules
	}
	return nil
}

func requestPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "/"
	}
	path := u.EscapedPath()
	if path == "" {
		return "/"
	}
	return path
}

func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
