import type { ReactNode } from 'react';
import { Link } from 'react-router';

const updated = '30 October 2026';
const contact = (
  <a href="https://github.com/RAdu2005/cupstream/issues" className="text-accent hover:underline">
    the CupStream issue tracker
  </a>
);

function LegalPage({ title, children }: { title: string; children: ReactNode }) {
  return (
    <article className="mx-auto w-full max-w-3xl space-y-6 p-4 leading-relaxed sm:p-8 [&_h2]:pt-2 [&_h2]:text-lg [&_h2]:font-semibold [&_li]:ml-5 [&_li]:list-disc [&_p]:text-fg/90 [&_ul]:space-y-1">
      <header className="space-y-1">
        <h1 className="text-3xl font-bold">{title}</h1>
        <p className="text-sm text-muted">Last updated {updated}</p>
      </header>
      {children}
    </article>
  );
}

export function Privacy() {
  return (
    <LegalPage title="Privacy policy">
      <p>
        CupStream is a live streaming service for sharing your screen with friends, run by Radu Stoiana as a personal
        project. This page explains what data it keeps and why. It keeps as little as it can: there are no ads, no
        analytics and no tracking cookies, and streams are never recorded.
      </p>

      <h2>Signing in with Google or Discord</h2>
      <p>You sign in with a Google or Discord account. CupStream never sees your password.</p>
      <ul>
        <li>
          <strong>Google</strong> (scopes <code>openid</code> and <code>profile</code>): your Google account ID, name and
          profile picture. CupStream does not request your email address and has no access to any other Google data.
        </li>
        <li>
          <strong>Discord</strong> (scopes <code>identify</code> and <code>guilds</code>): your Discord user ID, username,
          display name and avatar, and the list of servers you are in (their names, icons and approximate member counts).
          The server list lets CupStream suggest friends who share a server with you and show Discord rooms you can join.
          To keep that list current, CupStream stores a Discord refresh token, encrypted.
        </li>
      </ul>
      <p>
        Data received from Google and Discord is used only to provide CupStream to you. It is not sold, not used for
        advertising and not shared with anyone else.
      </p>

      <h2>What CupStream stores</h2>
      <ul>
        <li>Your account: handle, display name, avatar and the sign-in accounts linked to it.</li>
        <li>Sessions: a hashed session token, the device name of desktop-app sessions and when each session was last used.</li>
        <li>Rooms you own, who you have allowed into them, invites, bans, and your stream settings.</li>
        <li>A hashed stream key if you stream from OBS.</li>
        <li>A history of streams: who streamed in which room, when, and the peak number of viewers. Never the video or audio.</li>
        <li>Chat messages, which are deleted automatically after 24 hours.</li>
      </ul>

      <h2>Streams and network data</h2>
      <p>
        Video and audio travel directly between your device and the CupStream server over WebRTC, which means the server
        sees your IP address while you stream or watch. It is used to deliver the stream and may appear in server logs,
        which are kept for a short time for troubleshooting and are not shared.
      </p>

      <h2>Cookies</h2>
      <p>
        CupStream uses only the cookies it needs to work: a session cookie that keeps you signed in, and short-lived
        cookies that protect the sign-in process. No third-party cookies.
      </p>

      <h2>Service providers</h2>
      <ul>
        <li>Cloudflare handles web traffic to cupstream.larp.love and protects it from abuse.</li>
        <li>The server runs on a dedicated machine hosted by OVHcloud in the EU.</li>
        <li>Google and Discord handle sign-in under their own privacy policies.</li>
      </ul>

      <h2>Your choices</h2>
      <ul>
        <li>You can edit your handle and display name, unlink a sign-in account and end sessions on the settings page.</li>
        <li>
          You can remove CupStream's access at any time from your Google account (Security, third-party connections) or
          in Discord (User Settings, Authorized Apps).
        </li>
        <li>
          To get a copy of your data or have your account and all its data deleted, contact the operator through{' '}
          {contact}. Deletion is carried out within 30 days.
        </li>
      </ul>

      <h2>Changes</h2>
      <p>If this policy changes, the date at the top of this page changes with it.</p>

      <p className="text-sm text-muted">
        See also the <Link to="/terms" className="text-accent hover:underline">terms of service</Link>.
      </p>
    </LegalPage>
  );
}

export function Terms() {
  return (
    <LegalPage title="Terms of service">
      <p>
        CupStream is a personal project run by Radu Stoiana that lets you stream your screen to friends and watch their
        streams. By using it you agree to these terms.
      </p>

      <h2>Your account</h2>
      <ul>
        <li>You sign in with a Google or Discord account and are responsible for what happens under your CupStream account.</li>
        <li>Keep your stream key private; anyone who has it can stream as you.</li>
      </ul>

      <h2>What you stream</h2>
      <ul>
        <li>You are responsible for what you stream and post in chat, and you must have the right to share it.</li>
        <li>
          Do not stream or post anything illegal, sexual content involving minors, harassment, or content that infringes
          someone else's rights, and do not use CupStream to attack or overload other systems.
        </li>
        <li>Streams are live only and are not recorded by CupStream, but viewers can always capture what they see.</li>
      </ul>

      <h2>Discord</h2>
      <p>
        When you use CupStream with Discord, Discord's own terms and the rules of the server you are in also apply. Server
        administrators decide how CupStream may be used in their server.
      </p>

      <h2>The service</h2>
      <ul>
        <li>CupStream is free and provided as is, without guarantees of availability or quality.</li>
        <li>Features may change or be removed, and the service may stop at any time.</li>
        <li>
          Accounts or streams that break these terms may be suspended or removed. To the extent the law allows, the
          operator is not liable for damages arising from use of the service.
        </li>
      </ul>

      <h2>Contact</h2>
      <p>Questions about these terms can be sent through {contact}.</p>

      <p className="text-sm text-muted">
        See also the <Link to="/privacy" className="text-accent hover:underline">privacy policy</Link>.
      </p>
    </LegalPage>
  );
}
