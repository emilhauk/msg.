package handler

import (
	"net/http"
	"strings"

	"github.com/emilhauk/msg/internal/middleware"
	"github.com/emilhauk/msg/internal/model"
	redisclient "github.com/emilhauk/msg/internal/redis"
	"github.com/emilhauk/msg/internal/tmpl"
)

// RoomsHandler handles room page and room-management requests.
type RoomsHandler struct {
	Redis    *redisclient.Client
	Renderer *tmpl.Renderer
	BaseURL  string
	// JoinApprovers is the list of email addresses allowed to generate external
	// invite links when open registration is disabled.
	JoinApprovers []string
}

// canInviteExternal reports whether the given email is authorised to generate
// external invite links.
func (h *RoomsHandler) canInviteExternal(email string) bool {
	for _, a := range h.JoinApprovers {
		if strings.EqualFold(a, email) {
			return true
		}
	}
	return false
}

// HandleRoot redirects "/" to the first accessible room, or renders a
// "no rooms" page when the user has not yet been granted access to any room.
func (h *RoomsHandler) HandleRoot(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	rooms, err := h.Redis.GetAccessibleRooms(r.Context(), user.ID)
	if err == nil && len(rooms) > 0 {
		http.Redirect(w, r, "/rooms/"+rooms[0].ID, http.StatusFound)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "no-rooms.html", map[string]any{
		"User": user,
	})
}

type roomPageData struct {
	User     *model.User
	Room     *model.Room
	Rooms    []*model.Room // accessible rooms for the left sidebar
	Messages []*model.MessageView
	// OldestMS is the created_at ms timestamp of the oldest rendered message,
	// used as the cursor for infinite scroll.
	OldestMS string
}

// HandleRoom renders the room page with the last 50 messages.
func (h *RoomsHandler) HandleRoom(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	user := middleware.UserFromContext(r.Context())

	room, err := h.Redis.GetRoom(r.Context(), roomID)
	if err != nil || room == nil {
		h.Renderer.RenderError(w, http.StatusNotFound, tmpl.ErrorData{
			User:    user,
			Title:   "Room not found",
			Message: "This room does not exist or you do not have access to it.",
		})
		return
	}

	ok, err := h.Redis.IsRoomAccessible(r.Context(), roomID, user.ID)
	if err != nil || !ok {
		h.Renderer.RenderError(w, http.StatusForbidden, tmpl.ErrorData{
			User:    user,
			Title:   "Access denied",
			Message: "You don't have access to this room. Ask a member to invite you.",
		})
		return
	}

	msgs, err := h.Redis.GetLatestMessages(r.Context(), roomID, 50)
	if err != nil {
		http.Error(w, "failed to load messages", http.StatusInternalServerError)
		return
	}

	if err := hydrateMessages(r.Context(), h.Redis, msgs, user.ID); err != nil {
		http.Error(w, "failed to load message data", http.StatusInternalServerError)
		return
	}

	oldestMS := ""
	if len(msgs) > 0 {
		oldestMS = msgs[0].CreatedAtMS
	}

	views := make([]*model.MessageView, len(msgs))
	for i, m := range msgs {
		views[i] = &model.MessageView{Message: m, CurrentUserID: user.ID}
	}

	rooms, _ := h.Redis.GetAccessibleRooms(r.Context(), user.ID)

	h.Renderer.Render(w, http.StatusOK, "room.html", roomPageData{
		User:     user,
		Room:     room,
		Rooms:    rooms,
		Messages: views,
		OldestMS: oldestMS,
	})
}

// HandleCreate handles POST /rooms — creates a new private room.
func (h *RoomsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "room name is required", http.StatusBadRequest)
		return
	}
	room, err := h.Redis.CreateRoom(r.Context(), name, user.ID)
	if err != nil {
		http.Error(w, "failed to create room", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/rooms/"+room.ID, http.StatusSeeOther)
}

type roomPanelData struct {
	Room              *model.Room
	CurrentUserID     string
	Members           []model.RoomMemberStatus
	Candidates        []*model.User // users that can be invited (seen in other rooms)
	CanInviteExternal bool          // user is in JoinApprovers
	// InviteToken is set after HandleCreateInvite redirects back here.
	InviteToken string
}

// HandlePanel renders the room settings panel partial.
// GET /rooms/{id}/panel
func (h *RoomsHandler) HandlePanel(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	user := middleware.UserFromContext(r.Context())

	room, err := h.Redis.GetRoom(r.Context(), roomID)
	if err != nil || room == nil {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}
	ok, err := h.Redis.IsRoomAccessible(r.Context(), roomID, user.ID)
	if err != nil || !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	memberIDs, err := h.Redis.GetRoomAccessList(r.Context(), roomID)
	if err != nil {
		http.Error(w, "failed to load members", http.StatusInternalServerError)
		return
	}
	members, err := h.Redis.GetRoomMembersWithStatus(r.Context(), roomID, memberIDs)
	if err != nil {
		http.Error(w, "failed to load member status", http.StatusInternalServerError)
		return
	}

	candidates, _ := h.Redis.GetInviteCandidates(r.Context(), roomID, user.ID)

	canInviteExternal := false
	if len(h.JoinApprovers) > 0 {
		if fullUser, err := h.Redis.GetUser(r.Context(), user.ID); err == nil && fullUser != nil {
			canInviteExternal = h.canInviteExternal(fullUser.Email)
		}
	}

	// An invite token may be passed via query param after invite creation.
	inviteToken := r.URL.Query().Get("invite_url")

	h.Renderer.RenderPartial(w, http.StatusOK, "room-panel.html", roomPanelData{
		Room:              room,
		CurrentUserID:     user.ID,
		Members:           members,
		Candidates:        candidates,
		CanInviteExternal: canInviteExternal,
		InviteToken:       inviteToken,
	})
}

// HandleAddAccess handles POST /rooms/{id}/access — invites an existing user.
func (h *RoomsHandler) HandleAddAccess(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	user := middleware.UserFromContext(r.Context())

	ok, err := h.Redis.IsRoomAccessible(r.Context(), roomID, user.ID)
	if err != nil || !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	inviteeID := strings.TrimSpace(r.FormValue("user_id"))
	if inviteeID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	alreadyMember, err := h.Redis.IsRoomAccessible(r.Context(), roomID, inviteeID)
	if err != nil {
		http.Error(w, "failed to check membership", http.StatusInternalServerError)
		return
	}
	if alreadyMember {
		http.Error(w, "user already has access", http.StatusConflict)
		return
	}

	candidates, err := h.Redis.GetInviteCandidates(r.Context(), roomID, user.ID)
	if err != nil {
		http.Error(w, "failed to check candidates", http.StatusInternalServerError)
		return
	}
	isCandidate := false
	for _, c := range candidates {
		if c.ID == inviteeID {
			isCandidate = true
			break
		}
	}
	if !isCandidate {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := h.Redis.AddRoomAccess(r.Context(), roomID, inviteeID); err != nil {
		http.Error(w, "failed to add member", http.StatusInternalServerError)
		return
	}
	// Redirect back to the panel so HTMX refreshes it.
	http.Redirect(w, r, "/rooms/"+roomID+"/panel", http.StatusSeeOther)
}

// HandleCreateInvite handles POST /rooms/{id}/invites — generates an external
// invite link. Only users in JoinApprovers may call this.
func (h *RoomsHandler) HandleCreateInvite(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	user := middleware.UserFromContext(r.Context())

	ok, err := h.Redis.IsRoomAccessible(r.Context(), roomID, user.ID)
	if err != nil || !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	fullUser, err := h.Redis.GetUser(r.Context(), user.ID)
	if err != nil || fullUser == nil || !h.canInviteExternal(fullUser.Email) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	token, err := h.Redis.CreateInviteToken(r.Context(), roomID, user.ID)
	if err != nil {
		http.Error(w, "failed to create invite", http.StatusInternalServerError)
		return
	}
	// Redirect to the panel, passing the token so it can be displayed.
	http.Redirect(w, r, "/rooms/"+roomID+"/panel?invite_url="+token, http.StatusSeeOther)
}

// HandleJoin handles GET /join/{token} — accepts an invite link. The user must
// already be authenticated.
func (h *RoomsHandler) HandleJoin(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	user := middleware.UserFromContext(r.Context())

	roomID, found, err := h.Redis.ConsumeInviteToken(r.Context(), token)
	if err != nil {
		http.Error(w, "failed to process invite", http.StatusInternalServerError)
		return
	}
	if !found {
		h.Renderer.RenderError(w, http.StatusNotFound, tmpl.ErrorData{
			User:    user,
			Title:   "Invite not found",
			Message: "This invite link has expired or has already been used.",
		})
		return
	}
	if err := h.Redis.AddRoomAccess(r.Context(), roomID, user.ID); err != nil {
		http.Error(w, "failed to join room", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/rooms/"+roomID, http.StatusSeeOther)
}
