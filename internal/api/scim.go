package api

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v5"

	appauth "github.com/labstack/pitot/internal/auth"
	"github.com/labstack/pitot/internal/config"
)

const (
	scimContentType = "application/scim+json"
	scimUserSchema  = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimListSchema  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimPatchSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimErrorSchema = "urn:ietf:params:scim:api:messages:2.0:Error"

	scimRoot      = "/scim/v2"
	scimUsersPath = scimRoot + "/Users"

	// An OpenID Connect subject is at most 255 ASCII characters.
	scimMaxExternalID = 255
)

// SCIMHandler serves the narrow SCIM 2.0 subset used to provision accounts:
// create, look up, and patch a user. Requests are authenticated by the route
// policy in AuthMiddleware before they reach it.
type SCIMHandler struct {
	issuer     string
	publicURL  string
	users      *appauth.UserStore
	identities *appauth.IdentityStore
}

// RegisterSCIMRoutes registers the routes only when a token is configured.
func RegisterSCIMRoutes(e *echo.Echo, cfg config.Config, users *appauth.UserStore, identities *appauth.IdentityStore) {
	if cfg.SCIMToken == "" {
		return
	}
	h := &SCIMHandler{issuer: cfg.OIDCIssuerURL, publicURL: strings.TrimRight(cfg.PublicURL, "/"), users: users, identities: identities}
	e.GET("/scim/v2/Users", h.serve(h.listUsers))
	e.POST("/scim/v2/Users", h.serve(h.createUser))
	e.GET("/scim/v2/Users/:id", h.serve(h.getUser))
	e.PATCH("/scim/v2/Users/:id", h.serve(h.patchUser))
}

// scimError is an error with an RFC 7644 section 3.12 representation.
type scimError struct {
	status   int
	scimType string
	detail   string
}

func (e *scimError) Error() string { return e.detail }

func scimFail(status int, scimType, detail string) error {
	return &scimError{status: status, scimType: scimType, detail: detail}
}

func writeSCIM(c *echo.Context, status int, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	header := c.Response().Header()
	header.Set("Content-Type", scimContentType)
	header.Set("Cache-Control", "no-store")
	c.Response().WriteHeader(status)
	_, err = c.Response().Write(encoded)
	return err
}

func writeSCIMError(c *echo.Context, failure *scimError) error {
	body := map[string]any{
		"schemas": []string{scimErrorSchema},
		"detail":  failure.detail,
		"status":  strconv.Itoa(failure.status),
	}
	if failure.scimType != "" {
		body["scimType"] = failure.scimType
	}
	return writeSCIM(c, failure.status, body)
}

// serve renders every handler failure as a SCIM error.
func (h *SCIMHandler) serve(handler func(*echo.Context) error) echo.HandlerFunc {
	return func(c *echo.Context) error {
		err := handler(c)
		if err == nil {
			return nil
		}
		var failure *scimError
		if !errors.As(err, &failure) {
			slog.Error("SCIM request failed", "method", c.Request().Method, "route", c.Path(), "err", err)
			failure = &scimError{status: http.StatusInternalServerError, detail: "The request could not be completed."}
		}
		return writeSCIMError(c, failure)
	}
}

// authorizeSCIM is the route policy for SCIM paths. An authenticated request to
// a path or method the API does not serve gets a SCIM error rather than the
// product error format.
func authorizeSCIM(c *echo.Context, token string, classified bool, next echo.HandlerFunc) error {
	if !scimTokenMatches(scimBearerToken(c.Request().Header.Get("Authorization")), token) {
		c.Response().Header().Set("WWW-Authenticate", "Bearer")
		return writeSCIMError(c, &scimError{status: http.StatusUnauthorized, detail: "A valid bearer token is required."})
	}
	if classified {
		return next(c)
	}
	if _, known := classifySCIMRoute(http.MethodGet, c.Request().URL.Path); known {
		return writeSCIMError(c, &scimError{status: http.StatusMethodNotAllowed, detail: "This method is not supported."})
	}
	return writeSCIMError(c, &scimError{status: http.StatusNotFound, detail: "Not found."})
}

func scimBearerToken(header string) string {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// scimTokenMatches hashes both sides first, so the comparison takes the same
// time for every input, including one of a different length.
func scimTokenMatches(presented, expected string) bool {
	presentedSum, expectedSum := sha256.Sum256([]byte(presented)), sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(presentedSum[:], expectedSum[:]) == 1 && presented != ""
}

func isSCIMPath(path string) bool {
	return path == scimRoot || strings.HasPrefix(path, scimRoot+"/")
}

// scimBool accepts the string forms "True" and "False" as well as JSON
// booleans, because Microsoft Entra ID sends those for active and primary.
type scimBool bool

func (b *scimBool) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch typed := value.(type) {
	case bool:
		*b = scimBool(typed)
		return nil
	case string:
		switch {
		case strings.EqualFold(typed, "true"):
			*b = true
			return nil
		case strings.EqualFold(typed, "false"):
			*b = false
			return nil
		}
	}
	return errors.New("must be a boolean")
}

type scimMultiValue struct {
	Value   string    `json:"value"`
	Primary *scimBool `json:"primary"`
}

type scimUserRequest struct {
	Schemas     []string         `json:"schemas"`
	ExternalID  string           `json:"externalId"`
	UserName    string           `json:"userName"`
	DisplayName string           `json:"displayName"`
	Active      *scimBool        `json:"active"`
	Emails      []scimMultiValue `json:"emails"`
	Roles       []scimMultiValue `json:"roles"`
}

type scimPatchRequest struct {
	Schemas    []string `json:"schemas"`
	Operations []struct {
		Op    string          `json:"op"`
		Path  string          `json:"path"`
		Value json.RawMessage `json:"value"`
	} `json:"Operations"`
}

type scimResourceValue struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary"`
}

type scimMeta struct {
	ResourceType string `json:"resourceType"`
	Created      string `json:"created"`
	LastModified string `json:"lastModified"`
	Location     string `json:"location"`
}

type scimUserResource struct {
	Schemas     []string            `json:"schemas"`
	ID          string              `json:"id"`
	ExternalID  string              `json:"externalId,omitempty"`
	UserName    string              `json:"userName"`
	DisplayName string              `json:"displayName,omitempty"`
	Active      bool                `json:"active"`
	Emails      []scimResourceValue `json:"emails"`
	Roles       []scimResourceValue `json:"roles"`
	Meta        scimMeta            `json:"meta"`
}

func decodeSCIMBody(c *echo.Context, destination any) error {
	mediaType, _, err := mime.ParseMediaType(c.Request().Header.Get("Content-Type"))
	if err != nil || (mediaType != scimContentType && mediaType != "application/json") {
		return scimFail(http.StatusUnsupportedMediaType, "", "Content-Type must be application/scim+json.")
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, userBodyLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return scimFail(http.StatusRequestEntityTooLarge, "", "The request body is too large.")
		}
		return scimFail(http.StatusBadRequest, "invalidSyntax", "The request body could not be read.")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(destination); err != nil {
		return scimFail(http.StatusBadRequest, "invalidSyntax", "The request body is not a valid SCIM message.")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return scimFail(http.StatusBadRequest, "invalidSyntax", "The request body has trailing content.")
	}
	return nil
}

func hasSchema(schemas []string, want string) bool {
	for _, schema := range schemas {
		if strings.EqualFold(schema, want) {
			return true
		}
	}
	return false
}

// primaryValue returns the primary entry, or the only entry when none is marked.
func primaryValue(values []scimMultiValue) (string, bool) {
	var primary []string
	for _, value := range values {
		if value.Primary != nil && bool(*value.Primary) {
			primary = append(primary, value.Value)
		}
	}
	switch {
	case len(primary) == 1:
		return primary[0], true
	case len(primary) == 0 && len(values) == 1:
		return values[0].Value, true
	}
	return "", false
}

func parseSCIMRole(values []scimMultiValue) (appauth.Role, error) {
	value, ok := primaryValue(values)
	if !ok || !appauth.ValidRole(value) {
		return "", scimFail(http.StatusBadRequest, "invalidValue", "roles must hold one value: admin, operator, or viewer.")
	}
	return appauth.Role(value), nil
}

func parseSCIMEmails(values []scimMultiValue) (string, error) {
	value, ok := primaryValue(values)
	if !ok {
		return "", scimFail(http.StatusBadRequest, "invalidValue", "emails must hold one primary value.")
	}
	return parseSCIMEmail(value, "emails")
}

func parseSCIMEmail(value, attribute string) (string, error) {
	email, err := appauth.NormalizeEmail(value)
	if err != nil {
		return "", scimFail(http.StatusBadRequest, "invalidValue", attribute+" must be a valid email address.")
	}
	return email, nil
}

func scimAuditSource(c *echo.Context) appauth.AuditEvent {
	return appauth.AuditEvent{RemoteIP: c.RealIP(), UserAgent: c.Request().UserAgent(), Metadata: map[string]any{"mode": "scim"}}
}

func (h *SCIMHandler) createUser(c *echo.Context) error {
	var req scimUserRequest
	if err := decodeSCIMBody(c, &req); err != nil {
		return err
	}
	if !hasSchema(req.Schemas, scimUserSchema) {
		return scimFail(http.StatusBadRequest, "invalidSyntax", "schemas must include the core User schema.")
	}
	if strings.TrimSpace(req.ExternalID) == "" || len(req.ExternalID) > scimMaxExternalID {
		return scimFail(http.StatusBadRequest, "invalidValue", "externalId must be between 1 and 255 bytes.")
	}
	email, err := parseSCIMEmail(req.UserName, "userName")
	if err != nil {
		return err
	}
	if len(req.Emails) != 0 {
		primary, err := parseSCIMEmails(req.Emails)
		if err != nil {
			return err
		}
		if primary != email {
			return scimFail(http.StatusBadRequest, "invalidValue", "userName and the primary email must be the same address.")
		}
	}
	role, err := parseSCIMRole(req.Roles)
	if err != nil {
		return err
	}
	status := appauth.UserStatusActive
	if req.Active != nil && !bool(*req.Active) {
		status = appauth.UserStatusSuspended
	}

	user, identity, created, err := h.users.ProvisionFederated(c.Request().Context(), appauth.ProvisionRequest{
		Issuer: h.issuer, Subject: req.ExternalID, Email: email, DisplayName: req.DisplayName, Role: role, Status: status,
	}, scimAuditSource(c))
	switch {
	case errors.Is(err, appauth.ErrFirstUserMustBeAdmin):
		return scimFail(http.StatusConflict, "", "The first account must be an active administrator.")
	case errors.Is(err, appauth.ErrUserConflict), errors.Is(err, appauth.ErrIdentityConflict):
		return scimFail(http.StatusConflict, "uniqueness", "The email address belongs to a different account.")
	case err != nil:
		return err
	}
	resource := h.resource(user, identity.Subject)
	c.Response().Header().Set("Location", resource.Meta.Location)
	statusCode := http.StatusOK
	if created {
		statusCode = http.StatusCreated
	}
	return writeSCIM(c, statusCode, resource)
}

func (h *SCIMHandler) getUser(c *echo.Context) error {
	user, err := h.users.GetByIDContext(c.Request().Context(), c.Param("id"))
	if errors.Is(err, appauth.ErrUserNotFound) {
		return scimFail(http.StatusNotFound, "", "User not found.")
	}
	if err != nil {
		return err
	}
	subject, err := h.linkedSubject(c, user.ID)
	if err != nil {
		return err
	}
	return writeSCIM(c, http.StatusOK, h.resource(user, subject))
}

func (h *SCIMHandler) listUsers(c *echo.Context) error {
	attribute, value, ok := parseSCIMFilter(c.QueryParam("filter"))
	if !ok {
		return scimFail(http.StatusBadRequest, "invalidFilter", `Only externalId eq "value" and userName eq "value" are supported.`)
	}
	startIndex, err := scimQueryInt(c, "startIndex", 1)
	if err != nil {
		return err
	}
	count, err := scimQueryInt(c, "count", 1)
	if err != nil {
		return err
	}
	matches, err := h.lookup(c, attribute, value)
	if err != nil {
		return err
	}
	// A filter matches at most one account, so paging only decides whether
	// that account is on the requested page.
	response := map[string]any{
		"schemas": []string{scimListSchema}, "totalResults": len(matches),
		"startIndex": max(startIndex, 1), "itemsPerPage": 0,
	}
	if len(matches) > 0 && startIndex <= 1 && count > 0 {
		response["itemsPerPage"] = len(matches)
		response["Resources"] = matches
	}
	return writeSCIM(c, http.StatusOK, response)
}

func (h *SCIMHandler) lookup(c *echo.Context, attribute, value string) ([]scimUserResource, error) {
	ctx := c.Request().Context()
	var user appauth.User
	var subject string
	if attribute == "externalid" {
		identity, err := h.identities.Find(ctx, h.issuer, value)
		if errors.Is(err, appauth.ErrUserNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if user, err = h.users.GetByIDContext(ctx, identity.UserID); err != nil {
			return nil, err
		}
		subject = identity.Subject
	} else {
		// A userName that is not an address simply matches nothing.
		email, err := appauth.NormalizeEmail(value)
		if err != nil {
			return nil, nil
		}
		user, err = h.users.GetByEmailContext(ctx, email)
		if errors.Is(err, appauth.ErrUserNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if subject, err = h.linkedSubject(c, user.ID); err != nil {
			return nil, err
		}
	}
	return []scimUserResource{h.resource(user, subject)}, nil
}

// parseSCIMFilter accepts exactly `externalId eq "value"` and
// `userName eq "value"`. Attribute names and the operator are case-insensitive
// as RFC 7644 requires; the value is a JSON string, so escapes are decoded and
// anything after it is rejected.
func parseSCIMFilter(raw string) (attribute, value string, ok bool) {
	fields := strings.SplitN(strings.TrimSpace(raw), " ", 3)
	if len(fields) != 3 || !strings.EqualFold(fields[1], "eq") {
		return "", "", false
	}
	attribute = strings.ToLower(fields[0])
	if attribute != "externalid" && attribute != "username" {
		return "", "", false
	}
	literal := strings.TrimSpace(fields[2])
	if !utf8.ValidString(literal) || !strings.HasPrefix(literal, `"`) {
		return "", "", false
	}
	if err := json.Unmarshal([]byte(literal), &value); err != nil {
		return "", "", false
	}
	return attribute, value, true
}

func scimQueryInt(c *echo.Context, name string, fallback int) (int, error) {
	raw := c.QueryParam(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, scimFail(http.StatusBadRequest, "invalidValue", name+" must be an integer.")
	}
	return value, nil
}

func (h *SCIMHandler) patchUser(c *echo.Context) error {
	var req scimPatchRequest
	if err := decodeSCIMBody(c, &req); err != nil {
		return err
	}
	if !hasSchema(req.Schemas, scimPatchSchema) {
		return scimFail(http.StatusBadRequest, "invalidSyntax", "schemas must include the PatchOp schema.")
	}
	if len(req.Operations) == 0 {
		return scimFail(http.StatusBadRequest, "invalidValue", "Operations must not be empty.")
	}
	id := c.Param("id")
	subject, err := h.linkedSubject(c, id)
	if err != nil {
		return err
	}
	patch := scimPatch{subject: subject}
	for _, operation := range req.Operations {
		if err := patch.applyOperation(operation.Op, operation.Path, operation.Value); err != nil {
			return err
		}
	}
	if patch.userName != nil && patch.primaryEmail != nil && *patch.userName != *patch.primaryEmail {
		return scimFail(http.StatusBadRequest, "invalidValue", "userName and the primary email must be the same address.")
	}
	if patch.userName != nil {
		patch.change.Email = patch.userName
	} else if patch.primaryEmail != nil {
		patch.change.Email = patch.primaryEmail
	}
	user, _, err := h.users.UpdateProvisioned(id, patch.change, scimAuditSource(c))
	switch {
	case errors.Is(err, appauth.ErrUserNotFound):
		return scimFail(http.StatusNotFound, "", "User not found.")
	case errors.Is(err, appauth.ErrUserConflict):
		return scimFail(http.StatusConflict, "uniqueness", "The email address belongs to a different account.")
	case errors.Is(err, appauth.ErrLastActiveAdmin):
		return scimFail(http.StatusConflict, "", "This would remove the last active administrator.")
	case err != nil:
		return err
	}
	return writeSCIM(c, http.StatusOK, h.resource(user, subject))
}

// scimPatch collects the operations of one request, so they apply as a single
// update and the last-administrator check sees the final state.
type scimPatch struct {
	subject      string
	change       appauth.UserChange
	userName     *string
	primaryEmail *string
}

func (p *scimPatch) applyOperation(op, path string, value json.RawMessage) error {
	switch strings.ToLower(op) {
	case "replace", "add":
	case "remove":
		return scimFail(http.StatusBadRequest, "mutability", "Attributes cannot be removed; set a new value instead.")
	default:
		return scimFail(http.StatusBadRequest, "invalidSyntax", "op must be add, replace, or remove.")
	}
	if path != "" {
		return p.applyAttribute(path, value)
	}
	var attributes map[string]json.RawMessage
	if err := json.Unmarshal(value, &attributes); err != nil || attributes == nil {
		return scimFail(http.StatusBadRequest, "invalidValue", "An operation without a path needs an object value.")
	}
	for name, attribute := range attributes {
		if err := p.applyAttribute(name, attribute); err != nil {
			return err
		}
	}
	return nil
}

func (p *scimPatch) applyAttribute(name string, value json.RawMessage) error {
	switch strings.TrimPrefix(strings.ToLower(name), strings.ToLower(scimUserSchema)+":") {
	case "active":
		var active scimBool
		if err := json.Unmarshal(value, &active); err != nil {
			return scimFail(http.StatusBadRequest, "invalidValue", "active must be a boolean.")
		}
		status := appauth.UserStatusSuspended
		if active {
			status = appauth.UserStatusActive
		}
		p.change.Status = &status
	case "roles":
		var values []scimMultiValue
		if err := json.Unmarshal(value, &values); err != nil {
			return scimFail(http.StatusBadRequest, "invalidValue", "roles must be a list of values.")
		}
		role, err := parseSCIMRole(values)
		if err != nil {
			return err
		}
		p.change.Role = &role
	case "username":
		var userName string
		if err := json.Unmarshal(value, &userName); err != nil {
			return scimFail(http.StatusBadRequest, "invalidValue", "userName must be a string.")
		}
		email, err := parseSCIMEmail(userName, "userName")
		if err != nil {
			return err
		}
		p.userName = &email
	case "emails":
		var values []scimMultiValue
		if err := json.Unmarshal(value, &values); err != nil {
			return scimFail(http.StatusBadRequest, "invalidValue", "emails must be a list of values.")
		}
		email, err := parseSCIMEmails(values)
		if err != nil {
			return err
		}
		p.primaryEmail = &email
	case "displayname":
		var displayName string
		if err := json.Unmarshal(value, &displayName); err != nil {
			return scimFail(http.StatusBadRequest, "invalidValue", "displayName must be a string.")
		}
		p.change.DisplayName = &displayName
	case "externalid":
		var externalID string
		if err := json.Unmarshal(value, &externalID); err != nil || externalID != p.subject {
			return scimFail(http.StatusBadRequest, "mutability", "externalId cannot be changed.")
		}
	default:
		return scimFail(http.StatusBadRequest, "invalidPath", "This attribute cannot be patched.")
	}
	return nil
}

// linkedSubject returns the subject of the user's identity with the configured
// issuer, or an empty string when the user has none.
func (h *SCIMHandler) linkedSubject(c *echo.Context, userID string) (string, error) {
	identity, err := h.identities.FindForUser(c.Request().Context(), userID)
	if errors.Is(err, appauth.ErrUserNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if identity.Issuer != h.issuer {
		return "", nil
	}
	return identity.Subject, nil
}

func (h *SCIMHandler) resource(user appauth.User, subject string) scimUserResource {
	return scimUserResource{
		Schemas:     []string{scimUserSchema},
		ID:          user.ID,
		ExternalID:  subject,
		UserName:    user.Email,
		DisplayName: user.DisplayName,
		Active:      user.Status == appauth.UserStatusActive,
		Emails:      []scimResourceValue{{Value: user.Email, Primary: true}},
		Roles:       []scimResourceValue{{Value: string(user.Role), Primary: true}},
		Meta: scimMeta{
			ResourceType: "User",
			Created:      user.CreatedAt,
			LastModified: user.UpdatedAt,
			Location:     h.publicURL + scimUsersPath + "/" + user.ID,
		},
	}
}
