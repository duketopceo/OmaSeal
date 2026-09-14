package main

import (
	"errors"
	"fmt"
	"strings"
)

// maxComponentLen bounds service and account names, whether they arrive
// positionally or inside an omaseal:// reference.
const maxComponentLen = 256

// isOmaSealFamily reports whether arg claims the omaseal scheme, matched
// case-insensitively on the scheme token. Malformed family members
// (omaseal:x, omaseal:/x) count so they error clearly instead of silently
// becoming literal service names.
func isOmaSealFamily(arg string) bool {
	return len(arg) > len("omaseal:") && strings.EqualFold(arg[:len("omaseal:")], "omaseal:")
}

// parseRef splits "omaseal://<service>/<account>". The account may itself
// contain '/' (e.g. omaseal://browseros/openrouter-work/apiKey). A bare
// "omaseal://<service>" or trailing-slash form parses with an empty account;
// callers decide whether that is legal. strict enforces the shared charset;
// loose keeps reads permissive so entries written before validation existed
// (e.g. 1Password titles with spaces) stay reachable.
func parseRef(arg string, strict bool) (service, account string, err error) {
	if !isOmaSealFamily(arg) {
		return "", "", fmt.Errorf("malformed reference %q: want omaseal://<service>/<account>", arg)
	}
	rest := arg[len("omaseal"):]
	if !strings.HasPrefix(rest, "://") {
		return "", "", fmt.Errorf("malformed reference %q: want omaseal://<service>/<account>", arg)
	}
	rest = rest[len("://"):]
	service, account, _ = strings.Cut(rest, "/")
	if service == "" {
		return "", "", errors.New("reference has an empty service: want omaseal://<service>/<account>")
	}
	if strict {
		if err := validComponent("service", service, false); err != nil {
			return "", "", err
		}
		if account != "" {
			if err := validComponent("account", account, true); err != nil {
				return "", "", err
			}
		}
	}
	return service, account, nil
}

// refAwareCredentials resolves credentials on the agent surfaces (MCP/IPC):
// either separate service/account fields, or an omaseal:// reference carried
// in the service field — the form agents store and pass back verbatim.
// Validation failures carry a typed code so agents get a recovery path.
func refAwareCredentials(service, account string, strict bool) (string, string, error) {
	fail := func(err error) (string, string, error) {
		return "", "", newError("invalid_name", "omaseal set <service> <account>", err)
	}
	if isOmaSealFamily(service) {
		if account != "" {
			return "", "", fmt.Errorf("account must not accompany %s", service)
		}
		s, a, err := parseRef(service, strict)
		if err != nil {
			return fail(err)
		}
		if a == "" {
			return fail(fmt.Errorf("%s needs an account: want omaseal://<service>/<account>", service))
		}
		return s, a, nil
	}
	if service == "" || account == "" {
		return fail(errors.New("service and account are required"))
	}
	if strict {
		if err := validComponent("service", service, false); err != nil {
			return fail(err)
		}
		if err := validComponent("account", account, true); err != nil {
			return fail(err)
		}
	}
	return service, account, nil
}

// refAwareService resolves a service filter that may be a bare name or an
// omaseal://<service>[/] reference. A reference naming an account is an error.
func refAwareService(service string, strict bool) (string, error) {
	if !isOmaSealFamily(service) {
		return service, nil
	}
	s, a, err := parseRef(service, strict)
	if err != nil {
		return "", err
	}
	if a != "" {
		return "", fmt.Errorf("%s names an account; list filters by service only", service)
	}
	return s, nil
}

// argCredentials resolves the two CLI forms for set (strict) and
// get/reveal/del/resolve (loose):
//
//	omaseal <cmd> <service> <account>            positional
//	omaseal <cmd> omaseal://<service>/<account>  URI (account may contain '/')
func argCredentials(args []string) (service, account string, err error) {
	return parseCredentialArgs(args, true)
}

// argCredentialsLoose is the read-path variant: same structure without the
// charset check, so stored names that predate validation stay reachable.
func argCredentialsLoose(args []string) (service, account string, err error) {
	return parseCredentialArgs(args, false)
}

func parseCredentialArgs(args []string, strict bool) (service, account string, err error) {
	if len(args) == 1 && isOmaSealFamily(args[0]) {
		service, account, err = parseRef(args[0], strict)
		if err != nil {
			return "", "", err
		}
		if account == "" {
			return "", "", fmt.Errorf("%s needs an account: want omaseal://<service>/<account>", args[0])
		}
		return service, account, nil
	}
	for _, a := range args {
		if isOmaSealFamily(a) {
			return "", "", fmt.Errorf("%s is an omaseal:// reference: it must be the only argument", a)
		}
	}
	if len(args) != 2 {
		return "", "", errors.New("want <service> <account> or omaseal://<service>/<account>")
	}
	if strict {
		if err := validComponent("service", args[0], false); err != nil {
			return "", "", err
		}
		if err := validComponent("account", args[1], true); err != nil {
			return "", "", err
		}
	}
	return args[0], args[1], nil
}

// argService resolves the optional service filter for `list`: a bare service
// name, or omaseal://<service> with an optional trailing slash. A reference
// carrying an account is an error — list cannot filter by account.
func argService(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	if len(args) > 1 {
		return "", errors.New("list accepts at most one service argument")
	}
	return refAwareService(args[0], false)
}

// validComponent enforces the shared service/account alphabet: printable
// unreserved characters only, no whitespace, no control bytes, no '%' (no
// percent-encoding exists in this scheme, so accepting it would be a silent
// corruption path), and no '/' in services. Accounts may span segments.
func validComponent(kind, v string, allowSlash bool) error {
	if v == "" {
		return fmt.Errorf("%s must not be empty", kind)
	}
	if len(v) > maxComponentLen {
		return fmt.Errorf("%s exceeds %d bytes", kind, maxComponentLen)
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == '~' || c == '-' || (allowSlash && c == '/')
		if !ok {
			charset := "[A-Za-z0-9._~-]"
			if allowSlash {
				charset = "[A-Za-z0-9._~/-]"
			}
			return fmt.Errorf("%s %q: allowed characters are %s", kind, v, charset)
		}
	}
	return nil
}
