package connector

import (
	"fmt"
	"strconv"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/nukosuke/go-zendesk/zendesk"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func v1AnnotationsForResourceType(resourceTypeID string) annotations.Annotations {
	annos := annotations.Annotations{}
	annos.Update(&v2.V1Identifier{
		Id: resourceTypeID,
	})

	return annos
}

func parsePageToken(i string, resourceID *v2.ResourceId) (*pagination.Bag, int, error) {
	b := &pagination.Bag{}
	err := b.Unmarshal(i)
	if err != nil {
		return nil, 0, err
	}

	if b.Current() == nil {
		b.Push(pagination.PageState{
			ResourceTypeID: resourceID.ResourceType,
			ResourceID:     resourceID.Resource,
		})
	}

	page, err := convertPageToken(b.PageToken())
	if err != nil {
		return nil, 0, err
	}

	return b, page, nil
}

// convertPageToken converts a string token into an int.
func convertPageToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	return strconv.Atoi(token)
}

func titleCase(s string) string {
	titleCaser := cases.Title(language.English)

	return titleCaser.String(s)
}

// Populate entitlement options for zendesk resource.
func PopulateOptions(displayName, permission, resource string) []ent.EntitlementOption {
	options := []ent.EntitlementOption{
		ent.WithDisplayName(fmt.Sprintf("%s Role %s", displayName, permission)),
		ent.WithDescription(fmt.Sprintf("%s of Zendesk %s %s", permission, displayName, resource)),
		ent.WithGrantableTo(resourceTypeTeam, resourceTypeGroup),
	}
	return options
}

// getUserRoleResource creates a new connector resource for a Zendesk user.
func getUserRoleResource(user *zendesk.User, resourceTypeTeam *v2.ResourceType) (*v2.Resource, error) {
	firstname, lastname := splitFullName(user.Name)
	profile := map[string]interface{}{
		"user_id":    user.ID,
		"first_name": firstname,
		"last_name":  lastname,
		"login":      user.Email,
	}

	accountType := v2.UserTrait_ACCOUNT_TYPE_HUMAN
	var status v2.UserTrait_Status_Status
	switch user.Suspended {
	case true:
		status = v2.UserTrait_Status_STATUS_ENABLED
	case false:
		status = v2.UserTrait_Status_STATUS_DISABLED
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

// isValidTeamMember checks team members.
func isValidTeamMember(user *zendesk.User) bool {
	if user.Role == "agent" || user.Role == "admin" && !user.Suspended { // team member
		return true
	}

	return false
}

// GetUserSupportRoles gets user roles.
func GetUserSupportRoles(users []zendesk.User) map[string]int64 {
	var supportRoles = make(map[string]int64)
	for _, user := range users {
		userCopy := user
		if isValidTeamMember(&userCopy) { // only team member
			supportRoles[user.Role] = user.ID
		}
	}

	return supportRoles
}

// getTeamResource creates a new connector resource for a GitHub Team. It is possible that the team has a parent resource.
func getTeamResource(team *zendesk.User, resourceTypeTeam *v2.ResourceType, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"user_id":   team.ID,
		"user_name": team.Name,
	}

	ret, err := rs.NewGroupResource(
		team.Name,
		resourceTypeTeam,
		team.ID,
		[]rs.GroupTraitOption{rs.WithGroupProfile(profile)},
		rs.WithAnnotation(
			&v2.V1Identifier{Id: fmt.Sprintf("team_member:%d", team.ID)},
		),
		rs.WithParentResourceID(parentResourceID),
	)
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

// getUserByID gets an user by ID.
func getUserByID(userID int64, users map[int64]zendesk.User) zendesk.User {
	if user, ok := users[userID]; ok {
		return user
	}

	return zendesk.User{}
}

// getOrganizationMembers gets organization members.
func getOrganizationMembers(orgID int64, users map[int64]zendesk.User) []zendesk.User {
	var members []zendesk.User
	for _, user := range users {
		if user.OrganizationID == orgID {
			members = append(members, user)
		}
	}

	return members
}
