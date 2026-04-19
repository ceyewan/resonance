import { createRouter, createRoute, createRootRoute, Outlet, redirect } from '@tanstack/react-router';
import { LoginPage } from './features/auth/LoginPage';
import { RegisterPage } from './features/auth/RegisterPage';
import { ChatLayout } from './features/chat/ChatLayout';
import { EmptyChat } from './features/chat/EmptyChat';
import { ChatRoom } from './features/chat/ChatRoom';
import { ContactsPage } from './features/contact/ContactsPage';
import { SettingsPage } from './features/settings/SettingsPage';

const rootRoute = createRootRoute({
  component: () => (
    <>
      <Outlet />
    </>
  ),
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  beforeLoad: () => {
    // TanStack Router uses thrown redirects to short-circuit navigation.
    // eslint-disable-next-line @typescript-eslint/only-throw-error
    throw redirect({ to: '/chat' });
  },
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginPage,
});

const registerRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/register',
  component: RegisterPage,
});

const contactsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/contacts',
  component: ContactsPage,
});

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: SettingsPage,
});

const chatRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/chat',
  component: ChatLayout,
});

const chatIndexRoute = createRoute({
  getParentRoute: () => chatRoute,
  path: '/',
  component: EmptyChat,
});

const chatRoomRoute = createRoute({
  getParentRoute: () => chatRoute,
  path: '/$sessionId',
  component: ChatRoom,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  loginRoute,
  registerRoute,
  contactsRoute,
  settingsRoute,
  chatRoute.addChildren([chatIndexRoute, chatRoomRoute]),
]);

export const router = createRouter({ routeTree });

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
