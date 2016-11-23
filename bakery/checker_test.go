package bakery_test

import (
	"sort"
	"time"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

type dischargeRecord struct {
	location string
	user     string
}

type checkerSuite struct {
	jujutesting.LoggingSuite
	discharges []dischargeRecord
}

var _ = gc.Suite(&checkerSuite{})

func (s *checkerSuite) SetUpTest(c *gc.C) {
	s.discharges = nil
	s.LoggingSuite.SetUpTest(c)
}

func (s *checkerSuite) TestAuthorizeWithOpenAccessAndNoMacaroons(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("something"): {bakery.Everyone}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	authInfo, err := client.do(testContext, ts, readOp("something"))
	c.Assert(err, gc.IsNil)
	c.Assert(s.discharges, gc.HasLen, 0)
	c.Assert(authInfo, gc.NotNil)
	c.Assert(authInfo.Identity, gc.Equals, nil)
	c.Assert(authInfo.Macaroons, gc.HasLen, 0)
}

func (s *checkerSuite) TestAuthorizationDenied(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := bakery.ClosedAuthorizer
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	authInfo, err := client.do(asUser("bob"), ts, readOp("something"))
	c.Assert(err, gc.ErrorMatches, `permission denied`)
	c.Assert(authInfo, gc.IsNil)
}

func (s *checkerSuite) TestAuthorizeWithAuthenticationRequired(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("something"): {"bob"}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	authInfo, err := client.do(asUser("bob"), ts, readOp("something"))
	c.Assert(err, gc.IsNil)

	c.Assert(s.discharges, jc.DeepEquals, []dischargeRecord{{
		location: "ids",
		user:     "bob",
	}})
	c.Assert(authInfo, gc.NotNil)
	c.Assert(authInfo.Identity, gc.Equals, simpleIdentity("bob"))
	c.Assert(authInfo.Macaroons, gc.HasLen, 1)
}

func asUser(username string) context.Context {
	return contextWithDischargeUser(testContext, username)
}

func (s *checkerSuite) TestAuthorizeMultipleOps(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("something"): {"bob"}, readOp("otherthing"): {"bob"}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	_, err := client.do(asUser("bob"), ts, readOp("something"), readOp("otherthing"))
	c.Assert(err, gc.IsNil)

	c.Assert(s.discharges, jc.DeepEquals, []dischargeRecord{{
		location: "ids",
		user:     "bob",
	}})
}

func (s *checkerSuite) TestCapability(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("something"): {"bob"}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	m, err := client.dischargedCapability(asUser("bob"), ts, readOp("something"))
	c.Assert(err, gc.IsNil)

	// Check that we can exercise the capability directly on the service
	// with no discharging required.
	authInfo, err := ts.do(testContext, []macaroon.Slice{m}, readOp("something"))
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo, gc.NotNil)
	c.Assert(authInfo.Identity, gc.Equals, nil)
	c.Assert(authInfo.Macaroons, gc.HasLen, 1)
	c.Assert(authInfo.Macaroons[0][0].Id(), jc.DeepEquals, m[0].Id())
}

func (s *checkerSuite) TestCapabilityMultipleEntities(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("e1"): {"bob"}, readOp("e2"): {"bob"}, readOp("e3"): {"bob"}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	m, err := client.dischargedCapability(asUser("bob"), ts, readOp("e1"), readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)

	c.Assert(s.discharges, jc.DeepEquals, []dischargeRecord{{
		location: "ids",
		user:     "bob",
	}})

	// Check that we can exercise the capability directly on the service
	// with no discharging required.
	_, err = ts.do(testContext, []macaroon.Slice{m}, readOp("e1"), readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)

	// Check that we can exercise the capability to act on a subset of the operations.
	_, err = ts.do(testContext, []macaroon.Slice{m}, readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)
	_, err = ts.do(testContext, []macaroon.Slice{m}, readOp("e3"))
	c.Assert(err, gc.IsNil)
}

func (s *checkerSuite) TestMultipleCapabilities(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("e1"): {"alice"}, readOp("e2"): {"bob"}}
	ts := newService(auth, ids, locator)

	// Acquire two capabilities as different users and check
	// that we can combine them together to do both operations
	// at once.
	m1, err := newClient(locator).dischargedCapability(asUser("alice"), ts, readOp("e1"))
	c.Assert(err, gc.IsNil)
	m2, err := newClient(locator).dischargedCapability(asUser("bob"), ts, readOp("e2"))
	c.Assert(err, gc.IsNil)

	c.Assert(s.discharges, jc.DeepEquals, []dischargeRecord{{
		location: "ids",
		user:     "alice",
	}, {
		location: "ids",
		user:     "bob",
	}})

	authInfo, err := ts.do(testContext, []macaroon.Slice{m1, m2}, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)

	c.Assert(authInfo, gc.NotNil)
	c.Assert(authInfo.Identity, gc.Equals, nil)
	c.Assert(authInfo.Macaroons, gc.HasLen, 2)
	c.Assert(authInfo.Macaroons[0][0].Id(), jc.DeepEquals, m1[0].Id())
	c.Assert(authInfo.Macaroons[1][0].Id(), jc.DeepEquals, m2[0].Id())
}

func (s *checkerSuite) TestCombineCapabilities(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("e1"): {"alice"}, readOp("e2"): {"bob"}, readOp("e3"): {"bob", "alice"}}
	ts := newService(auth, ids, locator)

	// Acquire two capabilities as different users and check
	// that we can combine them together into a single capability
	// capable of both operations.
	m1, err := newClient(locator).dischargedCapability(asUser("alice"), ts, readOp("e1"), readOp("e3"))
	c.Assert(err, gc.IsNil)
	m2, err := newClient(locator).dischargedCapability(asUser("bob"), ts, readOp("e2"))
	c.Assert(err, gc.IsNil)

	m, err := ts.capability(testContext, []macaroon.Slice{m1, m2}, readOp("e1"), readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)

	_, err = ts.do(testContext, []macaroon.Slice{{m}}, readOp("e1"), readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)
}

func (s *checkerSuite) TestPartiallyAuthorizedRequest(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("e1"): {"alice"}, readOp("e2"): {"bob"}}
	ts := newService(auth, ids, locator)

	// Acquire a capability for e1 but rely on authentication to
	// authorize e2.
	m, err := newClient(locator).dischargedCapability(asUser("alice"), ts, readOp("e1"))
	c.Assert(err, gc.IsNil)

	client := newClient(locator)
	client.addMacaroon(ts, "authz", m)

	_, err = client.do(asUser("bob"), ts, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)
}

func (s *checkerSuite) TestAuthWithThirdPartyCaveats(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)

	// We make an authorizer that requires a third party discharge
	// when authorizing.
	auth := bakery.AuthorizerFunc(func(_ context.Context, id bakery.Identity, op bakery.Op) (bool, []checkers.Caveat, error) {
		if id == simpleIdentity("bob") && op == readOp("something") {
			return true, []checkers.Caveat{{
				Condition: "question",
				Location:  "other third party",
			}}, nil
		}
		return false, nil, nil
	})
	ts := newService(auth, ids, locator)

	locator["other third party"] = &discharger{
		key: mustGenerateKey(),
		checker: bakery.ThirdPartyCaveatCheckerFunc(func(ctxt context.Context, info *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			if info.Condition != "question" {
				return nil, errgo.Newf("third party condition not recognized")
			}
			s.discharges = append(s.discharges, dischargeRecord{
				location: "other third party",
				user:     dischargeUserFromContext(ctxt),
			})
			return nil, nil
		}),
		locator: locator,
	}

	client := newClient(locator)
	_, err := client.do(asUser("bob"), ts, readOp("something"))
	c.Assert(err, gc.IsNil)

	c.Assert(s.discharges, jc.DeepEquals, []dischargeRecord{{
		location: "ids",
		user:     "bob",
	}, {
		location: "other third party",
		user:     "bob",
	}})
}

func (s *checkerSuite) TestCapabilityCombinesFirstPartyCaveats(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("e1"): {"alice"}, readOp("e2"): {"bob"}}
	ts := newService(auth, ids, locator)

	// Acquire two capabilities as different users, add some first party caveats
	//
	// that we can combine them together into a single capability
	// capable of both operations.
	m1, err := newClient(locator).capability(asUser("alice"), ts, readOp("e1"))
	c.Assert(err, gc.IsNil)
	m1.AddFirstPartyCaveat("true 1")
	m1.AddFirstPartyCaveat("true 2")
	m2, err := newClient(locator).capability(asUser("bob"), ts, readOp("e2"))
	c.Assert(err, gc.IsNil)
	m2.AddFirstPartyCaveat("true 3")
	m2.AddFirstPartyCaveat("true 4")

	client := newClient(locator)
	client.addMacaroon(ts, "authz1", macaroon.Slice{m1})
	client.addMacaroon(ts, "authz2", macaroon.Slice{m2})

	m, err := client.capability(testContext, ts, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)

	c.Assert(macaroonConditions(m.Caveats(), false), jc.DeepEquals, []string{
		"true 1",
		"true 2",
		"true 3",
		"true 4",
	})
}

var firstPartyCaveatSquashingTests = []struct {
	about   string
	caveats []checkers.Caveat
	expect  []checkers.Caveat
}{{
	about: "duplicates removed",
	caveats: []checkers.Caveat{
		trueCaveat("1"),
		trueCaveat("2"),
		trueCaveat("1"),
		trueCaveat("2"),
		trueCaveat("3"),
	},
	expect: []checkers.Caveat{
		trueCaveat("1"),
		trueCaveat("2"),
		trueCaveat("3"),
	},
}, {
	about: "earliest time before",
	caveats: []checkers.Caveat{
		checkers.TimeBeforeCaveat(epoch.Add(24 * time.Hour)),
		trueCaveat("1"),
		checkers.TimeBeforeCaveat(epoch.Add(1 * time.Hour)),
		checkers.TimeBeforeCaveat(epoch.Add(5 * time.Minute)),
	},
	expect: []checkers.Caveat{
		checkers.TimeBeforeCaveat(epoch.Add(5 * time.Minute)),
		trueCaveat("1"),
	},
}, {
	about: "operations and declared caveats removed",
	caveats: []checkers.Caveat{
		checkers.DenyCaveat("foo"),
		checkers.AllowCaveat("read", "write"),
		checkers.DeclaredCaveat("username", "bob"),
		trueCaveat("1"),
	},
	expect: []checkers.Caveat{
		trueCaveat("1"),
	},
}}

func (s *checkerSuite) TestFirstPartyCaveatSquashing(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("e1"): {"alice"}, readOp("e2"): {"alice"}}
	ts := newService(auth, ids, locator)
	ns := ts.checker.Namespace()
	for i, test := range firstPartyCaveatSquashingTests {
		c.Logf("test %d: %v", i, test.about)

		// Make a first macaroon with all the required first party caveats.
		m1, err := newClient(locator).capability(asUser("alice"), ts, readOp("e1"))
		c.Assert(err, gc.IsNil)
		for _, cav := range test.caveats {
			m1.AddFirstPartyCaveat(ns.ResolveCaveat(cav).Condition)
		}

		// Make a second macaroon that's not used to check that it's
		// caveats are not added.
		m2, err := newClient(locator).capability(asUser("alice"), ts, readOp("e2"))
		c.Assert(err, gc.IsNil)
		m2.AddFirstPartyCaveat(ns.ResolveCaveat(trueCaveat("notused")).Condition)

		client := newClient(locator)
		client.addMacaroon(ts, "authz1", macaroon.Slice{m1})
		client.addMacaroon(ts, "authz2", macaroon.Slice{m2})

		m3, err := client.capability(testContext, ts, readOp("e1"))
		c.Assert(err, gc.IsNil)
		c.Assert(macaroonConditions(m3.Caveats(), false), jc.DeepEquals, resolveCaveats(ns, test.expect))
	}
}

func (s *checkerSuite) TestLoginOnly(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := bakery.ClosedAuthorizer
	ts := newService(auth, ids, locator)
	authInfo, err := newClient(locator).do(asUser("bob"), ts, bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, simpleIdentity("bob"))
}

func (s *checkerSuite) TestAllowAny(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("e1"): {"alice"}, readOp("e2"): {"bob"}}
	ts := newService(auth, ids, locator)

	// Acquire a capability for e1 but rely on authentication to
	// authorize e2.
	m, err := newClient(locator).dischargedCapability(asUser("alice"), ts, readOp("e1"))
	c.Assert(err, gc.IsNil)

	client := newClient(locator)
	client.addMacaroon(ts, "authz", m)

	s.discharges = nil
	authInfo, allowed, err := client.doAny(asUser("bob"), ts, readOp("e1"), readOp("e2"), bakery.LoginOp)
	c.Assert(err, gc.ErrorMatches, `discharge required`)
	c.Assert(authInfo, gc.NotNil)
	c.Assert(authInfo.Macaroons, gc.HasLen, 1)
	c.Assert(allowed, jc.DeepEquals, []bool{true, false, false})
	c.Assert(s.discharges, gc.HasLen, 0) // We shouldn't have discharged.

	// Log in as bob.
	_, err = client.do(asUser("bob"), ts, bakery.LoginOp)
	c.Assert(err, gc.IsNil)

	// All the previous actions should now be allowed.
	authInfo, allowed, err = client.doAny(asUser("bob"), ts, readOp("e1"), readOp("e2"), bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, simpleIdentity("bob"))
	c.Assert(authInfo.Macaroons, gc.HasLen, 2)
	c.Assert(allowed, jc.DeepEquals, []bool{true, true, true})
}

func (s *checkerSuite) TestAuthWithIdentityFromContext(c *gc.C) {
	locator := make(dischargerLocator)
	ids := basicAuthIdService{}
	auth := opAuthorizer{readOp("e1"): {"sherlock"}, readOp("e2"): {"bob"}}
	ts := newService(auth, ids, locator)

	// Check that we can perform the ops with basic auth in the
	// context.
	authInfo, err := newClient(locator).do(contextWithBasicAuth(testContext, "sherlock", "holmes"), ts, readOp("e1"))
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, simpleIdentity("sherlock"))
	c.Assert(authInfo.Macaroons, gc.HasLen, 0)
}

func (s *checkerSuite) TestOperationAllowCaveat(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("e1"): {"bob"}, writeOp("e1"): {"bob"}, readOp("e2"): {"bob"}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	m, err := client.capability(asUser("bob"), ts, readOp("e1"), writeOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)

	// Sanity check that we can do a write.
	_, err = ts.do(testContext, []macaroon.Slice{{m}}, writeOp("e1"))
	c.Assert(err, gc.IsNil)

	addFirstPartyCaveats(m, ts.checker.Namespace(), checkers.AllowCaveat("read"))

	// A read operation should work.
	_, err = ts.do(testContext, []macaroon.Slice{{m}}, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)

	// A write operation should fail even though the original macaroon allowed it.
	_, err = ts.do(testContext, []macaroon.Slice{{m}}, writeOp("e1"))
	c.Assert(err, gc.ErrorMatches, `discharge required`)
}

func (s *checkerSuite) TestOperationDenyCaveat(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opAuthorizer{readOp("e1"): {"bob"}, writeOp("e1"): {"bob"}, readOp("e2"): {"bob"}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	m, err := client.capability(asUser("bob"), ts, readOp("e1"), writeOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)

	// Sanity check that we can do a write.
	_, err = ts.do(testContext, []macaroon.Slice{{m}}, writeOp("e1"))
	c.Assert(err, gc.IsNil)

	addFirstPartyCaveats(m, ts.checker.Namespace(), checkers.DenyCaveat("write"))

	// A read operation should work.
	_, err = ts.do(testContext, []macaroon.Slice{{m}}, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)

	// A write operation should fail even though the original macaroon allowed it.
	_, err = ts.do(testContext, []macaroon.Slice{{m}}, writeOp("e1"))
	c.Assert(err, gc.ErrorMatches, `discharge required`)
}

func (s *checkerSuite) TestDuplicateLoginMacaroons(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := bakery.ClosedAuthorizer
	ts := newService(auth, ids, locator)

	// Acquire a login macaroon for bob.
	client1 := newClient(locator)
	authInfo, err := client1.do(asUser("bob"), ts, bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, simpleIdentity("bob"))

	// Acquire a login macaroon for alice.
	client2 := newClient(locator)
	authInfo, err = client2.do(asUser("alice"), ts, bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, simpleIdentity("alice"))

	// Combine the two login macaroons into one client.
	client3 := newClient(locator)
	client3.addMacaroon(ts, "1.bob", client1.macaroons[ts]["authn"])
	client3.addMacaroon(ts, "2.alice", client2.macaroons[ts]["authn"])

	// We should authenticate as bob (because macaroons are presented ordered
	// by "cookie" name)
	authInfo, err = client3.do(testContext, ts, bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, simpleIdentity("bob"))
	c.Assert(authInfo.Macaroons, gc.HasLen, 1)

	// Try them the other way around and we should authenticate as alice.
	client3 = newClient(locator)
	client3.addMacaroon(ts, "1.alice", client2.macaroons[ts]["authn"])
	client3.addMacaroon(ts, "2.bob", client1.macaroons[ts]["authn"])

	authInfo, err = client3.do(testContext, ts, bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, simpleIdentity("alice"))
	c.Assert(authInfo.Macaroons, gc.HasLen, 1)
}

func (s *checkerSuite) TestMacaroonOpsFatalError(c *gc.C) {
	// When we get a non-VerificationError error from the
	// opstore, we don't do any more verification.
	checker := bakery.NewChecker(bakery.CheckerParams{
		MacaroonOpStore: macaroonStoreWithError{errgo.New("an error")},
	})
	m, err := macaroon.New(nil, nil, "", macaroon.V2)
	c.Assert(err, gc.IsNil)
	_, err = checker.Auth(macaroon.Slice{m}).Allow(testContext, bakery.LoginOp)
	c.Assert(err, gc.ErrorMatches, `cannot retrieve macaroon: an error`)
}

func addFirstPartyCaveats(m *macaroon.Macaroon, ns *checkers.Namespace, caveats ...checkers.Caveat) {
	for _, cav := range caveats {
		m.AddFirstPartyCaveat(ns.ResolveCaveat(cav).Condition)
	}
}

// resolveCaveats resolves all the given caveats with the
// given namespace and includes the condition
// from each one. It will panic if it finds a third party caveat.
func resolveCaveats(ns *checkers.Namespace, caveats []checkers.Caveat) []string {
	conds := make([]string, len(caveats))
	for i, cav := range caveats {
		if cav.Location != "" {
			panic("found unexpected third party caveat")
		}
		conds[i] = ns.ResolveCaveat(cav).Condition
	}
	return conds
}

func macaroonConditions(caveats []macaroon.Caveat, allowThird bool) []string {
	conds := make([]string, len(caveats))
	for i, cav := range caveats {
		if cav.Location != "" {
			if !allowThird {
				panic("found unexpected third party caveat")
			}
			continue
		}
		conds[i] = string(cav.Id)
	}
	return conds
}

func firstPartyMacaroonCaveats(conds ...string) []macaroon.Caveat {
	caveats := make([]macaroon.Caveat, len(conds))
	for i, cond := range conds {
		caveats[i].Id = []byte(cond)
	}
	return caveats
}

func readOp(entity string) bakery.Op {
	return bakery.Op{
		Entity: entity,
		Action: "read",
	}
}

func writeOp(entity string) bakery.Op {
	return bakery.Op{
		Entity: entity,
		Action: "write",
	}
}

// opAuthorizer implements bakery.Authorizer by looking the operation
// up in the given map. If the username is in the associated slice
// or the slice contains "everyone", authorization is granted.
type opAuthorizer map[bakery.Op][]string

func (auth opAuthorizer) Authorize(ctxt context.Context, id bakery.Identity, ops []bakery.Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	return bakery.ACLAuthorizer{
		AllowPublic: true,
		GetACL: func(ctxt context.Context, op bakery.Op) ([]string, error) {
			return auth[op], nil
		},
	}.Authorize(ctxt, id, ops)
}

type idService struct {
	location string
	*discharger
	suite *checkerSuite
}

func (s *checkerSuite) newIdService(location string, locator dischargerLocator) *idService {
	ids := &idService{
		location: location,
		suite:    s,
	}
	key := mustGenerateKey()
	ids.discharger = &discharger{
		key:     key,
		checker: ids,
		locator: locator,
	}
	locator[location] = ids.discharger
	return ids
}

func (ids *idService) CheckThirdPartyCaveat(ctxt context.Context, info *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	if info.Condition != "is-authenticated-user" {
		return nil, errgo.Newf("third party condition not recognized")
	}
	username := dischargeUserFromContext(ctxt)
	if username == "" {
		return nil, errgo.Newf("no current user")
	}
	ids.suite.discharges = append(ids.suite.discharges, dischargeRecord{
		location: ids.location,
		user:     username,
	})
	return []checkers.Caveat{
		checkers.DeclaredCaveat("username", username),
	}, nil
}

func (ids *idService) IdentityFromContext(ctxt context.Context) (bakery.Identity, []checkers.Caveat, error) {
	return nil, []checkers.Caveat{{
		Location:  ids.location,
		Condition: "is-authenticated-user",
	}}, nil
}

func (ids *idService) DeclaredIdentity(declared map[string]string) (bakery.Identity, error) {
	user, ok := declared["username"]
	if !ok {
		return nil, errgo.Newf("no username declared")
	}
	return simpleIdentity(user), nil
}

type dischargeUserKey struct{}

func contextWithDischargeUser(ctxt context.Context, username string) context.Context {
	return context.WithValue(ctxt, dischargeUserKey{}, username)
}

func dischargeUserFromContext(ctxt context.Context) string {
	username, _ := ctxt.Value(dischargeUserKey{}).(string)
	return username
}

var _ bakery.ACLIdentity = simpleIdentity("")

type simpleIdentity string

func (simpleIdentity) Domain() string {
	return ""
}

func (id simpleIdentity) Id() string {
	return string(id)
}

func (id simpleIdentity) Allow(ctxt context.Context, acl []string) (bool, error) {
	for _, g := range acl {
		if string(id) == g {
			return true, nil
		}
	}
	return false, nil
}

type basicAuthIdService struct{}

func (basicAuthIdService) IdentityFromContext(ctxt context.Context) (bakery.Identity, []checkers.Caveat, error) {
	user, pass := basicAuthFromContext(ctxt)
	if user != "sherlock" || pass != "holmes" {
		return nil, nil, nil
	}
	return simpleIdentity(user), nil, nil
}

func (basicAuthIdService) DeclaredIdentity(declared map[string]string) (bakery.Identity, error) {
	return nil, errgo.Newf("no identity declarations in basic auth id service")
}

// service represents a service that requires authorization.
// Clients can make requests to the service to perform operations
// and may receive a macaroon to discharge if the authorization
// process requires it.
type service struct {
	checker *bakery.Checker
	store   *macaroonStore
}

func newService(auth bakery.Authorizer, idm bakery.IdentityClient, locator bakery.ThirdPartyLocator) *service {
	store := newMacaroonStore(mustGenerateKey(), locator)
	return &service{
		checker: bakery.NewChecker(bakery.CheckerParams{
			Checker:         testChecker,
			Authorizer:      auth,
			IdentityClient:  idm,
			MacaroonOpStore: store,
		}),
		store: store,
	}
}

// do makes a request to the service to perform the given operations
// using the given macaroons for authorization.
// It may return a dischargeRequiredError containing a macaroon
// that needs to be discharged.
func (svc *service) do(ctxt context.Context, ms []macaroon.Slice, ops ...bakery.Op) (*bakery.AuthInfo, error) {
	authInfo, err := svc.checker.Auth(ms...).Allow(ctxt, ops...)
	return authInfo, svc.maybeDischargeRequiredError(err)
}

// doAny makes a request to the service to perform any of the given
// operations. It reports which operations have succeeded.
func (svc *service) doAny(ctxt context.Context, ms []macaroon.Slice, ops ...bakery.Op) (*bakery.AuthInfo, []bool, error) {
	authInfo, allowed, err := svc.checker.Auth(ms...).AllowAny(ctxt, ops...)
	return authInfo, allowed, svc.maybeDischargeRequiredError(err)
}

func (svc *service) capability(ctxt context.Context, ms []macaroon.Slice, ops ...bakery.Op) (*macaroon.Macaroon, error) {
	conds, err := svc.checker.Auth(ms...).AllowCapability(ctxt, ops...)
	if err != nil {
		return nil, svc.maybeDischargeRequiredError(err)
	}
	m, err := svc.store.NewMacaroon(ctxt, ops, nil, svc.checker.Namespace())
	if err != nil {
		return nil, errgo.Mask(err)
	}
	for _, cond := range conds {
		if err := m.AddFirstPartyCaveat(cond); err != nil {
			return nil, errgo.Mask(err)
		}
	}
	return m, nil
}

func (svc *service) maybeDischargeRequiredError(err error) error {
	derr, ok := errgo.Cause(err).(*bakery.DischargeRequiredError)
	if !ok {
		return errgo.Mask(err)
	}
	m, err := svc.store.NewMacaroon(testContext, derr.Ops, derr.Caveats, svc.checker.Namespace())
	if err != nil {
		return errgo.Mask(err)
	}
	name := "authz"
	if len(derr.Ops) == 1 && derr.Ops[0] == bakery.LoginOp {
		name = "authn"
	}
	return &dischargeRequiredError{
		name: name,
		m:    m,
	}
}

type discharger struct {
	key     *bakery.KeyPair
	locator bakery.ThirdPartyLocator
	checker bakery.ThirdPartyCaveatChecker
}

type dischargeRequiredError struct {
	name string
	m    *macaroon.Macaroon
}

func (*dischargeRequiredError) Error() string {
	return "discharge required"
}

func (d *discharger) discharge(ctxt context.Context, cav macaroon.Caveat, ns *checkers.Namespace) (*macaroon.Macaroon, error) {
	m, caveats, err := bakery.Discharge(ctxt, d.key, d.checker, cav.Id)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	for _, cav := range caveats {
		if err := bakery.AddCaveat(ctxt, d.key, d.locator, m, cav, ns); err != nil {
			return nil, errgo.Mask(err)
		}
	}
	return m, nil
}

type dischargerLocator map[string]*discharger

// ThirdPartyInfo implements the bakery.ThirdPartyLocator interface.
func (l dischargerLocator) ThirdPartyInfo(ctxt context.Context, loc string) (bakery.ThirdPartyInfo, error) {
	d, ok := l[loc]
	if !ok {
		return bakery.ThirdPartyInfo{}, bakery.ErrNotFound
	}
	return bakery.ThirdPartyInfo{
		PublicKey: d.key.Public,
		Version:   bakery.LatestVersion,
	}, nil
}

type client struct {
	key         *bakery.KeyPair
	macaroons   map[*service]map[string]macaroon.Slice
	dischargers dischargerLocator
}

func newClient(dischargers dischargerLocator) *client {
	return &client{
		key:         mustGenerateKey(),
		dischargers: dischargers,
		// macaroons holds the macaroons applicable to each service.
		// This is the test equivalent of the cookie jar used by httpbakery.
		macaroons: make(map[*service]map[string]macaroon.Slice),
	}
}

const maxRetries = 3

// do performs a set of operations on the given service.
// It includes all the macaroons in c.macaroons[svc] as authorization
// information on the request.
func (c *client) do(ctxt context.Context, svc *service, ops ...bakery.Op) (*bakery.AuthInfo, error) {
	var authInfo *bakery.AuthInfo
	err := c.doFunc(ctxt, svc, func(ms []macaroon.Slice) (err error) {
		authInfo, err = svc.do(ctxt, ms, ops...)
		return
	})
	return authInfo, err
}

func (c *client) doAny(ctxt context.Context, svc *service, ops ...bakery.Op) (*bakery.AuthInfo, []bool, error) {
	return svc.doAny(ctxt, c.requestMacaroons(svc), ops...)
}

// capability returns a capability macaroon for the given operations.
func (c *client) capability(ctxt context.Context, svc *service, ops ...bakery.Op) (*macaroon.Macaroon, error) {
	var m *macaroon.Macaroon
	err := c.doFunc(ctxt, svc, func(ms []macaroon.Slice) (err error) {
		m, err = svc.capability(ctxt, ms, ops...)
		return
	})
	return m, err
}

func (c *client) dischargedCapability(ctxt context.Context, svc *service, ops ...bakery.Op) (macaroon.Slice, error) {
	m, err := c.capability(ctxt, svc, ops...)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return c.dischargeAll(ctxt, m, svc.checker.Namespace())
}

func (c *client) doFunc(ctxt context.Context, svc *service, f func(ms []macaroon.Slice) error) error {
	for i := 0; i < maxRetries; i++ {
		err := f(c.requestMacaroons(svc))
		derr, ok := errgo.Cause(err).(*dischargeRequiredError)
		if !ok {
			return err
		}
		ms, err := c.dischargeAll(ctxt, derr.m, svc.checker.Namespace())
		if err != nil {
			return errgo.Mask(err)
		}
		c.addMacaroon(svc, derr.name, ms)
	}
	return errgo.New("discharge failed too many times")
}

func (c *client) clearMacaroons(svc *service) {
	if svc == nil {
		c.macaroons = make(map[*service]map[string]macaroon.Slice)
		return
	}
	delete(c.macaroons, svc)
}

func (c *client) addMacaroon(svc *service, name string, m macaroon.Slice) {
	if c.macaroons[svc] == nil {
		c.macaroons[svc] = make(map[string]macaroon.Slice)
	}
	c.macaroons[svc][name] = m
}

func (c *client) requestMacaroons(svc *service) []macaroon.Slice {
	mmap := c.macaroons[svc]
	// Put all the macaroons in the slice ordered by key
	// so that we have deterministic behaviour in the tests.
	names := make([]string, 0, len(mmap))
	for name := range mmap {
		names = append(names, name)
	}
	sort.Strings(names)
	ms := make([]macaroon.Slice, len(names))
	for i, name := range names {
		logger.Infof("macaroon %d: %v", i, name)
		ms[i] = mmap[name]
	}
	return ms
}

func (c *client) dischargeAll(ctxt context.Context, m *macaroon.Macaroon, ns *checkers.Namespace) (macaroon.Slice, error) {
	return bakery.DischargeAll(ctxt, m, func(cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		d := c.dischargers[cav.Location]
		if d == nil {
			return nil, errgo.Newf("third party discharger %q not found", cav.Location)
		}
		return d.discharge(ctxt, cav, ns)
	})
}
