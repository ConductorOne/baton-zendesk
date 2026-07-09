package connector

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/nukosuke/go-zendesk/zendesk"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// isNotFoundError reports whether err represents a missing resource. Covers three shapes:
//   - the raw Zendesk 404 HTTP response (zendesk.Error with status 404)
//   - the client-layer wrapped form (gRPC codes.NotFound from wrapZendeskError)
//   - the client-layer sentinel ErrMembershipNotFound returned when a pre-DELETE
//     membership lookup finds no matching record
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, client.ErrMembershipNotFound) {
		return true
	}
	var zErr zendesk.Error
	if errors.As(err, &zErr) && zErr.Status() == http.StatusNotFound {
		return true
	}
	return status.Code(err) == codes.NotFound
}

// isAlreadyExistsError reports whether err is Zendesk's 422 RecordInvalid/DuplicateValue
// response returned when creating a group_membership or organization_membership that
// already exists. Anchored on the "DuplicateValue" error code (verified live) with
// "has already been taken" as a defensive fallback.
func isAlreadyExistsError(err error) bool {
	var zErr zendesk.Error
	if !errors.As(err, &zErr) {
		return false
	}
	if zErr.Status() != http.StatusUnprocessableEntity {
		return false
	}
	msg := zErr.Error()
	return strings.Contains(msg, "DuplicateValue") ||
		strings.Contains(msg, "has already been taken")
}

const (
	userIDKey      = "user_id"
	successKey     = "success"
	userKey        = "user"
	roleDisplay    = "Role"
)

func v1AnnotationsForResourceType(resourceTypeID string) annotations.Annotations {
	annos := annotations.Annotations{}
	annos.Update(&v2.V1Identifier{
		Id: resourceTypeID,
	})

	return annos
}

// withSkipEntitlements appends the SkipEntitlements annotation to the given
// resource-type annotations. It tells the SDK to skip the per-resource
// Entitlements() call for this resource type while still invoking
// StaticEntitlements(). Use it for resource types whose entitlements are
// identical across every resource (e.g. org, role): the SDK materializes the
// static entitlements against each resource locally, avoiding one connector
// round-trip per resource. Grants are unaffected — they reference entitlements
// by the same canonical ID either way.
func withSkipEntitlements(annos annotations.Annotations) annotations.Annotations {
	annos.Update(&v2.SkipEntitlements{})
	return annos
}

func titleCase(s string) string {
	titleCaser := cases.Title(language.English)

	return titleCaser.String(s)
}

// getUserRoleResource creates a new connector resource for a Zendesk user.
func getUserRoleResource(user *zendesk.User, resourceTypeTeam *v2.ResourceType) (*v2.Resource, error) {
	firstname, lastname := splitFullName(user.Name)
	profile := map[string]interface{}{
		userIDKey:   user.ID,
		"first_name": firstname,
		"last_name":  lastname,
		"login":      user.Email,
	}

	accountType := v2.UserTrait_ACCOUNT_TYPE_HUMAN
	var status v2.UserTrait_Status_Status
	switch user.Suspended {
	case true:
		status = v2.UserTrait_Status_STATUS_DISABLED
	case false:
		status = v2.UserTrait_Status_STATUS_ENABLED
	default:
		status = v2.UserTrait_Status_STATUS_UNSPECIFIED
	}

	userTraitOptions := []rs.UserTraitOption{
		rs.WithUserProfile(profile),
		rs.WithEmail(user.Email, true),
		rs.WithStatus(status),
		rs.WithAccountType(accountType),
	}

	ret, err := rs.NewUserResource(
		user.Name,
		resourceTypeTeam,
		user.ID,
		userTraitOptions,
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// splitFullName returns firstName and lastName.
func splitFullName(name string) (string, string) {
	names := strings.SplitN(name, " ", 2)
	var firstName, lastName string

	switch len(names) {
	case 1:
		firstName = names[0]
	case 2:
		firstName = names[0]
		lastName = names[1]
	}

	return firstName, lastName
}

func getTeamResource(user *zendesk.User, resourceTypeTeam *v2.ResourceType) (*v2.Resource, error) {
	var userStatus = v2.UserTrait_Status_STATUS_ENABLED
	firstName, lastName := splitFullName(user.Name)
	profile := map[string]interface{}{
		userIDKey:   fmt.Sprint(user.ID),
		"login":      user.Email,
		"first_name": firstName,
		"last_name":  lastName,
		"email":      user.Email,
	}
	if !user.Active || user.Suspended {
		userStatus = v2.UserTrait_Status_STATUS_DISABLED
	}

	userTraits := []rs.UserTraitOption{
		rs.WithUserProfile(profile),
		rs.WithStatus(userStatus),
		rs.WithUserLogin(user.Email),
		rs.WithEmail(user.Email, true),
	}

	if user.LastLoginAt.String() != "" {
		loginTime, err := time.Parse("2006-01-02T15:04:05Z", user.LastLoginAt.String())
		if err == nil {
			userTraits = append(userTraits, rs.WithLastLogin(loginTime))
		}
	}

	if user.CreatedAt.String() != "" {
		createdAt, err := time.Parse("2006-01-02T15:04:05.000000Z", user.CreatedAt.String())
		if err == nil {
			userTraits = append(userTraits, rs.WithCreatedAt(createdAt))
		}
	}

	displayName := user.Name
	if user.Name == "" {
		displayName = user.Email
	}

	ret, err := rs.NewUserResource(displayName, resourceTypeTeam, user.ID, userTraits)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// getGroupResource gets a new connector resource for a Zenddesk group.
func getGroupResource(group zendesk.Group, resourceTypeGroup *v2.ResourceType, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"group_id":   group.ID,
		"group_name": group.Name,
	}
	groupTraitOptions := []rs.GroupTraitOption{rs.WithGroupProfile(profile)}
	ret, err := rs.NewGroupResource(
		group.Name,
		resourceTypeGroup,
		group.ID,
		groupTraitOptions,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// getUserResource gets a new connector resource for a Zenddesk group.
func getUserResource(user zendesk.User, resourceTypeUser *v2.ResourceType) (*v2.Resource, error) {
	resource, err := rs.NewUserResource(user.Name, resourceTypeUser, user.ID, nil)
	if err != nil {
		return nil, err
	}

	return resource, nil
}

// getUserByID looks up a user by ID in the provided map.
// Returns an error if the user is not found, so the caller can fall back to a live API call.
func getUserByID(userID int64, users map[int64]zendesk.User) (zendesk.User, error) {
	if user, ok := users[userID]; ok {
		return user, nil
	}
	return zendesk.User{}, fmt.Errorf("user %d not found in cache", userID)
}

// getRoleResource creates a new connector resource for a Zendesk role.
func getRoleResource(role *zendesk.CustomRole, resourceTypeRole *v2.ResourceType, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"role_id":   role.ID,
		"role_name": role.Name,
	}

	roleTraitOptions := []rs.RoleTraitOption{
		rs.WithRoleProfile(profile),
	}

	ret, err := rs.NewRoleResource(
		role.Name,
		resourceTypeRole,
		role.ID,
		roleTraitOptions,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}
