import { useAuthStore } from "@/stores/auth";
import { useWebSocket } from "@/hooks/useWebSocket";
import LoginPage from "@/pages/LoginPage";
import ChatPage from "@/pages/ChatPage";

function App() {
  const { isAuthenticated, accessToken } = useAuthStore();

  // WebSocket hook 只在登录后才会连接（因为有 token）
  const { isConnected, send } = useWebSocket({
    token: accessToken ?? undefined,
    onMessage: (packet) => {
      // Handle WebSocket messages
      console.log("[App] Received packet:", packet);
    },
  });

  return (
    <div className="h-screen w-screen bg-background text-foreground">
      {!isAuthenticated ? <LoginPage /> : <ChatPage isConnected={isConnected} send={send} />}
    </div>
  );
}

export default App;
