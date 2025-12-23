import { useEffect } from "react";
import { useAuthStore } from "@/stores/auth";
import { useSessionStore } from "@/stores/session";
import { useWebSocket } from "@/hooks/useWebSocket";
import LoginPage from "@/pages/LoginPage";
import ChatPage from "@/pages/ChatPage";

function App() {
  const { isAuthenticated } = useAuthStore();
  const { isConnected, connect } = useWebSocket({
    onMessage: (packet) => {
      // Handle WebSocket messages
      console.log("[App] Received packet:", packet);
    },
  });

  // Connect WebSocket when authenticated
  useEffect(() => {
    if (isAuthenticated && !isConnected) {
      connect();
    }
  }, [isAuthenticated, isConnected, connect]);

  return (
    <div className="h-screen w-screen bg-background text-foreground">
      {!isAuthenticated ? <LoginPage /> : <ChatPage />}
    </div>
  );
}

export default App;
