import type { RouteObject } from 'react-router';
import { AppShell } from './components/AppShell';
import { AppAuthorize } from './pages/AppAuthorize';
import { Home } from './pages/Home';
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
      { path: 'app/authorize', element: <AppAuthorize /> },
      { path: 'settings', element: <Settings /> },
      { path: 'r/:slug', element: <Room /> },
      { path: 'privacy', element: <Privacy /> },
      { path: 'terms', element: <Terms /> },
      { path: '*', element: <NotFound /> },
    ],
  },
];
