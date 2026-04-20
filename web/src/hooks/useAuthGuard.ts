import { useEffect, useRef } from "react";
import { useNavigate } from "@tanstack/react-router";

import { restoreAuthSession } from "../services/auth";
import { useAuthState } from "./useAuthState";

export function useAuthGuard() {
  const auth = useAuthState();
  const navigate = useNavigate();
  const restoredRef = useRef(false);

  useEffect(() => {
    if (restoredRef.current) {
      return;
    }
    restoredRef.current = true;
    void restoreAuthSession().catch(() => {
      // 失败由 auth store 暴露给页面决定如何显示。
    });
  }, []);

  useEffect(() => {
    if (!auth.authenticated && !auth.bootstrapping && !auth.bootstrapError) {
      void navigate({ to: "/login" });
    }
  }, [auth.authenticated, auth.bootstrapping, auth.bootstrapError, navigate]);

  return auth;
}
