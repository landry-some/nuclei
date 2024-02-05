package kerberos

import (
	"strings"

	"github.com/dop251/goja"
	kclient "github.com/jcmturner/gokrb5/v8/client"
	kconfig "github.com/jcmturner/gokrb5/v8/config"
	"github.com/jcmturner/gokrb5/v8/iana/errorcode"
	"github.com/jcmturner/gokrb5/v8/messages"
	"github.com/projectdiscovery/nuclei/v3/pkg/js/utils"
	"github.com/projectdiscovery/nuclei/v3/pkg/protocols/common/protocolstate"
	ConversionUtil "github.com/projectdiscovery/utils/conversion"
)

// EnumerateUserResponse is the response from EnumerateUser
type EnumerateUserResponse struct {
	Valid     bool   `json:"valid"`
	ASREPHash string `json:"asrep_hash"`
	Error     string `json:"error"`
}

// TGS is the response from GetServiceTicket
type TGS struct {
	Ticket messages.Ticket `json:"ticket"`
	Hash   string          `json:"hash"`
	ErrMsg string          `json:"error"`
}

// Config is extra configuration for the kerberos client
type Config struct {
	ip      string
	timeout int // in seconds
}

func (c *Config) SetIPAddress(ip string) *Config {
	c.ip = ip
	return c
}

func (c *Config) SetTimeout(timeout int) *Config {
	c.timeout = timeout
	return c
}

// Example Values for jargons
// Realm: ACME.COM (Authentical zone / security area)
// Domain: acme.com (Public website / domain)
// DomainController: dc.acme.com (Domain Controller / Active Directory Server)
// KDC: kdc.acme.com (Key Distribution Center / Authentication Server)

// Updated Package definations and structure
type Client struct {
	nj         *utils.NucleiJS // helper functions/bindings
	Krb5Config *kconfig.Config
	Realm      string
	config     Config
}

// Constructor for KerberosClient
// if controller is empty a dns lookup for default kdc server will be performed
// Signature: Client(domain, {controller})
// @param domain: string
// @param controller: string (optional)
// When controller is empty or not given krb5 will perform a DNS lookup for the default KDC server
// and retrieve its address from the DNS server
func NewKerberosClient(call goja.ConstructorCall, runtime *goja.Runtime) *goja.Object {
	// setup nucleijs utils
	c := &Client{nj: utils.NewNucleiJS(runtime)}
	c.nj.ObjectSig = "Client(domain, controller)" // will be included in error messages

	// get arguments (type assertion is efficient than reflection)
	// when accepting type as input like net.Conn we can use utils.GetArg
	domain, _ := c.nj.GetArg(call.Arguments, 0).(string)
	controller, _ := c.nj.GetArg(call.Arguments, 1).(string)

	// validate arguments
	c.nj.Require(domain != "", "domain cannot be empty")

	cfg := kconfig.New()

	if controller != "" {
		// validate controller hostport
		if !protocolstate.IsHostAllowed(controller) {
			c.nj.Throw("domain controller address blacklisted by network policy")
		}

		tmp := strings.Split(controller, ":")
		if len(tmp) == 1 {
			tmp = append(tmp, "88")
		}
		realm := strings.ToUpper(domain)
		cfg.LibDefaults.DefaultRealm = realm // set default realm
		cfg.Realms = []kconfig.Realm{
			{
				Realm:         realm,
				KDC:           []string{tmp[0] + ":" + tmp[1]},
				AdminServer:   []string{tmp[0] + ":" + tmp[1]},
				KPasswdServer: []string{tmp[0] + ":464"}, // default password server port
			},
		}
		cfg.DomainRealm = make(kconfig.DomainRealm)
	} else {
		// if controller is empty use DNS lookup
		cfg.LibDefaults.DNSLookupKDC = true
		cfg.LibDefaults.DefaultRealm = strings.ToUpper(domain)
		cfg.DomainRealm = make(kconfig.DomainRealm)
	}
	c.Krb5Config = cfg
	c.Realm = strings.ToUpper(domain)

	// Link Constructor to Client and return
	return utils.LinkConstructor(call, runtime, c)
}

// NewKerberosClientFromString creates a new kerberos client from a string
// by parsing krb5.conf
// @param cfg: string
// Example krb5.conf:
// [libdefaults]
// default_realm = ACME.COM
// dns_lookup_kdc = true
func NewKerberosClientFromString(cfg string) (*Client, error) {
	config, err := kconfig.NewFromString(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{Krb5Config: config}, nil
}

// SetConfig sets additional config for the kerberos client
// Signature: SetConfig(cfg)
// @param cfg: @Config
// Note: as of now ip and timeout overrides are only supported
// in EnumerateUser due to fastdialer but can be extended to other methods currently
func (c *Client) SetConfig(cfg *Config) {
	if cfg == nil {
		c.nj.Throw("config cannot be nil")
	}
	c.config = *cfg
}

// EnumerateUser and attempt to get AS-REP hash by disabling PA-FX-FAST
// Signature: EnumerateUser(username, {password})
// @param username: string
func (c *Client) EnumerateUser(username string) (EnumerateUserResponse, error) {
	c.nj.Require(c.Krb5Config != nil, "Kerberos client not initialized")
	password := "password"
	// client does not actually attempt connection it manages state here
	client := kclient.NewWithPassword(username, c.Realm, password, c.Krb5Config, kclient.DisablePAFXFAST(true))
	defer client.Destroy()

	// generate ASReq hash
	req, err := messages.NewASReqForTGT(client.Credentials.Domain(), client.Config, client.Credentials.CName())
	c.nj.HandleError(err, "failed to generate TGT request")

	// marshal request
	b, err := req.Marshal()
	c.nj.HandleError(err, "failed to marshal TGT request")

	data, err := SendToKDC(c, string(b))
	rb := ConversionUtil.Bytes(data)

	if err == nil {
		var ASRep messages.ASRep
		resp := EnumerateUserResponse{Valid: true}
		err = ASRep.Unmarshal(rb)
		if err != nil {
			resp.Error = err.Error()
			return resp, nil
		}
		hashcatString, _ := ASRepToHashcat(ASRep)
		resp.ASREPHash = hashcatString
		return resp, nil
	}

	resp := EnumerateUserResponse{}
	e, ok := err.(messages.KRBError)
	if !ok {
		return resp, err
	}
	if e.ErrorCode == errorcode.KDC_ERR_PREAUTH_REQUIRED {
		resp.Valid = true
		resp.Error = errorcode.Lookup(e.ErrorCode)
		return resp, nil
	}
	resp.Error = errorcode.Lookup(e.ErrorCode)
	return resp, nil
}

// GetServiceTicket returns a TGS for a given user, password, target and SPN
// Signature: GetServiceTicket(User, Pass, Target, SPN)
// @param User: string
// @param Pass: string
// @param SPN: string Service Principal Name
func (c *Client) GetServiceTicket(User, Pass, SPN string) (TGS, error) {
	c.nj.Require(c.Krb5Config != nil, "Kerberos client not initialized")
	c.nj.Require(User != "", "User cannot be empty")
	c.nj.Require(Pass != "", "Pass cannot be empty")
	c.nj.Require(SPN != "", "SPN cannot be empty")

	if len(c.Krb5Config.Realms) > 0 {
		// this means dc address was given
		for _, r := range c.Krb5Config.Realms {
			for _, kdc := range r.KDC {
				if !protocolstate.IsHostAllowed(kdc) {
					c.nj.Throw("KDC address blacklisted by network policy")
				}
			}
			for _, kpasswd := range r.KPasswdServer {
				if !protocolstate.IsHostAllowed(kpasswd) {
					c.nj.Throw("Kpasswd address blacklisted by network policy")
				}
			}
		}
	} else {
		// here net.Dialer is used instead of fastdialer hence get possible addresses
		// and check if they are allowed by network policy
		_, kdcs, _ := c.Krb5Config.GetKDCs(c.Realm, true)
		for _, v := range kdcs {
			if !protocolstate.IsHostAllowed(v) {
				c.nj.Throw("KDC address blacklisted by network policy")
			}
		}
	}

	// client does not actually attempt connection it manages state here
	client := kclient.NewWithPassword(User, c.Realm, Pass, c.Krb5Config, kclient.DisablePAFXFAST(true))
	defer client.Destroy()

	resp := TGS{}

	ticket, _, err := client.GetServiceTicket(SPN)
	resp.Ticket = ticket
	if err != nil {
		if code, ok := err.(messages.KRBError); ok {
			resp.ErrMsg = errorcode.Lookup(code.ErrorCode)
			return resp, err
		}
		return resp, err
	}
	// convert AS-REP to hashcat format
	hashcat, err := TGStoHashcat(ticket, c.Realm)
	if err != nil {
		if code, ok := err.(messages.KRBError); ok {
			resp.ErrMsg = errorcode.Lookup(code.ErrorCode)
			return resp, err
		}
		return resp, err
	}
	resp.Ticket = ticket
	resp.Hash = hashcat
	return resp, nil
}

// GetASREP returns AS-REP for a given user and password
// it contains Client's TGT , Principal and Session Key
// Signature: GetASREP(User, Pass)
// @param User: string
// @param Pass: string
func (c *Client) GetASREP(User, Pass string) messages.ASRep {
	c.nj.Require(c.Krb5Config != nil, "Kerberos client not initialized")
	c.nj.Require(User != "", "User cannot be empty")
	c.nj.Require(Pass != "", "Pass cannot be empty")

	if len(c.Krb5Config.Realms) > 0 {
		// this means dc address was given
		for _, r := range c.Krb5Config.Realms {
			for _, kdc := range r.KDC {
				if !protocolstate.IsHostAllowed(kdc) {
					c.nj.Throw("KDC address blacklisted by network policy")
				}
			}
			for _, kpasswd := range r.KPasswdServer {
				if !protocolstate.IsHostAllowed(kpasswd) {
					c.nj.Throw("Kpasswd address blacklisted by network policy")
				}
			}
		}
	} else {
		// here net.Dialer is used instead of fastdialer hence get possible addresses
		// and check if they are allowed by network policy
		_, kdcs, _ := c.Krb5Config.GetKDCs(c.Realm, true)
		for _, v := range kdcs {
			if !protocolstate.IsHostAllowed(v) {
				c.nj.Throw("KDC address blacklisted by network policy")
			}
		}
	}

	// login to get TGT
	cl := kclient.NewWithPassword(User, c.Realm, Pass, c.Krb5Config, kclient.DisablePAFXFAST(true))
	defer cl.Destroy()

	// generate ASReq
	ASReq, err := messages.NewASReqForTGT(cl.Credentials.Domain(), cl.Config, cl.Credentials.CName())
	c.nj.HandleError(err, "failed to generate TGT request")

	// exchange AS-REQ for AS-REP
	resp, err := cl.ASExchange(c.Realm, ASReq, 0)
	c.nj.HandleError(err, "failed to exchange AS-REQ")

	// try to decrypt encrypted parts of the response and TGT
	key, err := resp.DecryptEncPart(cl.Credentials)
	if err == nil {
		_ = resp.Ticket.Decrypt(key)
	}
	return resp
}
