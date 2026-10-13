package access

import (
	"fmt"
	"testing"
)

func TestCheck(t *testing.T) {
	everyone := Room{Kind: Link, OwnerID: "owner", Visibility: Everyone}
	signedIn := Room{Kind: Link, OwnerID: "owner", Visibility: SignedIn}
	inviteOnly := Room{Kind: Link, OwnerID: "owner", Visibility: InviteOnly}
	discordVideo := Room{Kind: Discord, Policy: VideoPermission}
	discordAnyone := Room{Kind: Discord, Policy: AnyoneInChannel}

	anon := Subject{}
	owner := Subject{UserID: "owner"}
	user := Subject{UserID: "u"}
	allowed := Subject{UserID: "u", Allowed: true}
	coStreamer := Subject{UserID: "u", Allowed: true, AllowedToStream: true}
	pass := Subject{UserID: "u", Pass: true}
	streamPass := Subject{UserID: "u", Pass: true, PassToStream: true}
	banned := Subject{UserID: "u", Banned: true, Allowed: true, Pass: true}
	bannedOwner := Subject{UserID: "owner", Banned: true}

	noDiscord := Subject{UserID: "u"}
	notInVoice := Subject{UserID: "u", DiscordLinked: true}
	inVoice := Subject{UserID: "u", DiscordLinked: true, InVoiceChannel: true}
	inVoiceVideo := Subject{UserID: "u", DiscordLinked: true, InVoiceChannel: true, CanVideo: true}
	mover := Subject{UserID: "u", DiscordLinked: true, InVoiceChannel: true, CanMoveMembers: true}
	msgMod := Subject{UserID: "u", DiscordLinked: true, InVoiceChannel: true, CanManageMessages: true}
	modNotInVoice := Subject{UserID: "u", DiscordLinked: true, CanMoveMembers: true, CanManageMessages: true, CanVideo: true}

	cases := []struct {
		name    string
		room    Room
		subject Subject
		action  Action
		want    Reason
	}{
		// Link room, everyone.
		{"everyone: anonymous watches", everyone, anon, Watch, OK},
		{"everyone: anonymous cannot chat", everyone, anon, Chat, SignInRequired},
		{"everyone: user chats", everyone, user, Chat, OK},
		{"everyone: user cannot stream", everyone, user, Stream, NotAllowed},
		{"everyone: anonymous cannot stream", everyone, anon, Stream, SignInRequired},
		{"everyone: banned cannot watch", everyone, banned, Watch, Banned},
		{"everyone: user cannot moderate", everyone, user, ModerateStreams, NotAllowed},

		// Link room, signed in.
		{"signed_in: anonymous must sign in", signedIn, anon, Watch, SignInRequired},
		{"signed_in: user watches", signedIn, user, Watch, OK},
		{"signed_in: user chats", signedIn, user, Chat, OK},
		{"signed_in: banned refused", signedIn, banned, Watch, Banned},

		// Link room, invite only.
		{"invite_only: anonymous must sign in", inviteOnly, anon, Watch, SignInRequired},
		{"invite_only: stranger refused", inviteOnly, user, Watch, NotAllowed},
		{"invite_only: stranger cannot chat", inviteOnly, user, Chat, NotAllowed},
		{"invite_only: allowed watches", inviteOnly, allowed, Watch, OK},
		{"invite_only: allowed chats", inviteOnly, allowed, Chat, OK},
		{"invite_only: allowed cannot stream", inviteOnly, allowed, Stream, NotAllowed},
		{"invite_only: co-streamer streams", inviteOnly, coStreamer, Stream, OK},
		{"invite_only: pass watches", inviteOnly, pass, Watch, OK},
		{"invite_only: pass cannot stream", inviteOnly, pass, Stream, NotAllowed},
		{"invite_only: streaming pass streams", inviteOnly, streamPass, Stream, OK},
		{"invite_only: ban beats allowed list and pass", inviteOnly, banned, Watch, Banned},
		{"invite_only: banned cannot stream", inviteOnly, Subject{UserID: "u", Banned: true, AllowedToStream: true}, Stream, Banned},

		// Owner can do everything in their own room, whatever else is set.
		{"owner watches", inviteOnly, owner, Watch, OK},
		{"owner streams", inviteOnly, owner, Stream, OK},
		{"owner moderates streams", everyone, owner, ModerateStreams, OK},
		{"owner moderates chat", signedIn, owner, ModerateChat, OK},
		{"owner is never banned from own room", inviteOnly, bannedOwner, Watch, OK},
		{"co-streamer cannot moderate", inviteOnly, coStreamer, ModerateStreams, NotAllowed},

		// Discord rooms.
		{"discord: anonymous must sign in", discordVideo, anon, Watch, SignInRequired},
		{"discord: needs linked Discord", discordVideo, noDiscord, Watch, DiscordRequired},
		{"discord: must join voice", discordVideo, notInVoice, Watch, JoinVoiceChannel},
		{"discord: in voice watches", discordVideo, inVoice, Watch, OK},
		{"discord: in voice chats", discordVideo, inVoice, Chat, OK},
		{"discord: no video permission", discordVideo, inVoice, Stream, NoVideoPerm},
		{"discord: video permission streams", discordVideo, inVoiceVideo, Stream, OK},
		{"discord: anyone-in-channel policy", discordAnyone, inVoice, Stream, OK},
		{"discord: move members moderates streams", discordVideo, mover, ModerateStreams, OK},
		{"discord: move members cannot moderate chat", discordVideo, mover, ModerateChat, NotAllowed},
		{"discord: manage messages moderates chat", discordVideo, msgMod, ModerateChat, OK},
		{"discord: manage messages cannot end streams", discordVideo, msgMod, ModerateStreams, NotAllowed},
		{"discord: permissions need voice presence", discordVideo, modNotInVoice, ModerateStreams, JoinVoiceChannel},
		{"discord: video needs voice presence", discordAnyone, modNotInVoice, Stream, JoinVoiceChannel},
		{"discord: link-room facts are ignored", discordVideo, Subject{UserID: "u", Allowed: true, Pass: true}, Watch, DiscordRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Check(tc.room, tc.subject, tc.action); got != tc.want {
				t.Errorf("Check(%s) = %q, want %q", tc.action, got, tc.want)
			}
		})
	}
}

func ExampleCheck() {
	room := Room{Kind: Link, OwnerID: "alice", Visibility: InviteOnly}
	fmt.Println(Check(room, Subject{UserID: "bob"}, Watch))
	fmt.Println(Check(room, Subject{UserID: "bob", Allowed: true}, Watch) == OK)
	// Output:
	// not_allowed
	// true
}
