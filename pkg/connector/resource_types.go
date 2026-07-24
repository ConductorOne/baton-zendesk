package connector

import v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"

// OrgResourceTypeID is the "org" resource type ID. Exported so callers (e.g.
// team_member.go's cross-type grant emission) can gate on it via
// cli.ConnectorOpts.WillSyncResourceType without risking drift from
// resourceTypeOrg.Id.
const OrgResourceTypeID = "org"

var (
	resourceTypeGroup = &v2.ResourceType{
		Id:          "group",
		DisplayName: "Group",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_GROUP,
		},
	}
	resourceTypeOrg = &v2.ResourceType{
		Id:          OrgResourceTypeID,
		DisplayName: "Org",
		Annotations: withSkipGrants(withSkipEntitlements(v1AnnotationsForResourceType("org"))),
	}
	resourceTypeRole = &v2.ResourceType{
		Id:          "role",
		DisplayName: roleDisplay,
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_ROLE,
		},
		Annotations: withSkipEntitlements(nil),
	}
	resourceTypeTeam = &v2.ResourceType{
		Id:          "team_member",
		DisplayName: "Team Member",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_USER,
		},
		Annotations: v1AnnotationsForResourceType("team_member"),
	}
)
