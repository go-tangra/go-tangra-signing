// Package contacts is the signing module's view of the tenant's users, asked
// from the auth service over the mesh (research D1): the e-mail address and
// display name of signers (auth.v1.Profiles/Contacts, admitted by auth's
// policy for svc/signing only) and the member list with names for the signer
// picker (ListMembers + Lookup). E-mail addresses are used for invitations and
// certificate subjects and are never sent to the browser.
package contacts

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"

	authv1 "github.com/go-tangra/go-tangra-auth/sdk/v4/api/proto/auth/v1"
)

// ErrUnavailable is returned when auth cannot be asked.
var ErrUnavailable = errors.New("contacts: auth unavailable")

// Contact is one member's contact.
type Contact struct {
	UserID, DisplayName, Email string
}

// Member is one member for the picker (no e-mail).
type Member struct {
	UserID, DisplayName string
}

// Directory answers the module's questions about users.
type Directory interface {
	// Contacts returns the active members among ids that have an e-mail, keyed by user id.
	Contacts(ctx context.Context, tenant string, ids []string) (map[string]Contact, error)
	// Members lists active members whose display name contains q (at most limit).
	Members(ctx context.Context, tenant, q string, limit int) ([]Member, error)
}

// Limits of the auth RPCs.
const (
	lookupBatch  = 100
	membersBatch = 1000
	maxScan      = 5000 // members scanned for a picker query
)

// Client asks auth over the mesh.
type Client struct {
	Dial    func(ctx context.Context) (grpc.ClientConnInterface, error)
	Timeout time.Duration
}

func (c Client) profiles(ctx context.Context) (authv1.ProfilesClient, context.Context, context.CancelFunc, error) {
	t := c.Timeout
	if t <= 0 {
		t = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	conn, err := c.Dial(ctx)
	if err != nil {
		cancel()
		return nil, nil, nil, ErrUnavailable
	}
	return authv1.NewProfilesClient(conn), ctx, cancel, nil
}

func unique(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Contacts implements Directory.
func (c Client) Contacts(ctx context.Context, tenant string, ids []string) (map[string]Contact, error) {
	ids = unique(ids)
	out := map[string]Contact{}
	if len(ids) == 0 {
		return out, nil
	}
	pc, ctx, cancel, err := c.profiles(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	for i := 0; i < len(ids); i += lookupBatch {
		end := min(i+lookupBatch, len(ids))
		res, err := pc.Contacts(ctx, &authv1.LookupContactsRequest{TenantId: tenant, UserIds: ids[i:end]})
		if err != nil {
			return nil, ErrUnavailable
		}
		for _, ct := range res.GetContacts() {
			out[ct.GetUserId()] = Contact{UserID: ct.GetUserId(), DisplayName: ct.GetDisplayName(), Email: ct.GetEmail()}
		}
	}
	return out, nil
}

// Members implements Directory: pages active member ids, resolves their names
// and filters by q (case-insensitive), sorted by name.
func (c Client) Members(ctx context.Context, tenant, q string, limit int) ([]Member, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	pc, ctx, cancel, err := c.profiles(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	q = strings.ToLower(strings.TrimSpace(q))
	var out []Member
	cursor := ""
	for scanned := 0; scanned < maxScan; {
		page, err := pc.ListMembers(ctx, &authv1.ListMembersRequest{TenantId: tenant, Cursor: cursor, Limit: membersBatch})
		if err != nil {
			return nil, ErrUnavailable
		}
		ids := page.GetUserIds()
		scanned += len(ids)
		for i := 0; i < len(ids); i += lookupBatch {
			end := min(i+lookupBatch, len(ids))
			res, err := pc.Lookup(ctx, &authv1.LookupProfilesRequest{TenantId: tenant, UserIds: ids[i:end]})
			if err != nil {
				return nil, ErrUnavailable
			}
			for _, p := range res.GetProfiles() {
				if q == "" || strings.Contains(strings.ToLower(p.GetDisplayName()), q) {
					out = append(out, Member{UserID: p.GetUserId(), DisplayName: p.GetDisplayName()})
				}
			}
		}
		if cursor = page.GetNextCursor(); cursor == "" {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].DisplayName), strings.ToLower(out[j].DisplayName)
		if a != b {
			return a < b
		}
		return out[i].UserID < out[j].UserID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Fake is an in-memory Directory for tests: users by tenant.
type Fake struct {
	Users map[string][]Contact // tenant → users
	Err   error
}

// Contacts implements Directory.
func (f *Fake) Contacts(_ context.Context, tenant string, ids []string) (map[string]Contact, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := map[string]Contact{}
	for _, u := range f.Users[tenant] {
		if want[u.UserID] && u.Email != "" {
			out[u.UserID] = u
		}
	}
	return out, nil
}

// Members implements Directory.
func (f *Fake) Members(_ context.Context, tenant, q string, limit int) ([]Member, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	var out []Member
	for _, u := range f.Users[tenant] {
		if q == "" || strings.Contains(strings.ToLower(u.DisplayName), strings.ToLower(q)) {
			out = append(out, Member{UserID: u.UserID, DisplayName: u.DisplayName})
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

var (
	_ Directory = Client{}
	_ Directory = (*Fake)(nil)
)
