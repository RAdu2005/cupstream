import type { RouteObject } from 'react-router';
import { AppShell } from './components/AppShell';
import { Home } from './pages/Home';
import { Invite } from './pages/Invite';
import { Privacy, Terms } from './pages/Legal';
import { Login } from './pages/Login';
import { NotFound } from './pages/NotFound';
import { Room } from './pages/Room';
import { Settings } from './pages/Settings';

export const routes: RouteObject[] = [
  {
    element: <AppShell />,
    children: [
      { index: true, element: <Home /> },
      { path: 'login', element: <Login /> },
      { path: 'settings', element: <Settings /> },
      { path: 'r/:slug', element: <Room /> },
      { path: 'i/:token', element: <Invite /> },
      { path: 'privacy', element: <Privacy /> },
      { path: 'terms', element: <Terms /> },
      { path: '*', element: <NotFound /> },
    ],
  },
];
