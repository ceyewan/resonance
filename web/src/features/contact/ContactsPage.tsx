import { FormEvent, useMemo, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { GlassCard } from "../../components/GlassCard";
import { GlassInput } from "../../components/GlassInput";
import { WallpaperBackground } from "../../components/WallpaperBackground";
import { useAuthGuard } from "../../hooks/useAuthGuard";
import { useContactDirectory } from "../../hooks/useContactDirectory";

export function ContactsPage() {
  const auth = useAuthGuard();
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [groupName, setGroupName] = useState("");
  const [groupMembers, setGroupMembers] = useState("");
  const [busyAction, setBusyAction] = useState("");
  const [actionError, setActionError] = useState("");
  const directory = useContactDirectory();

  const visibleResults = useMemo(
    () => (query.trim() === "" ? directory.contacts : directory.searchResults),
    [directory.contacts, directory.searchResults, query],
  );

  const runAction = async (name: string, fn: () => Promise<void>) => {
    setBusyAction(name);
    setActionError("");
    try {
      await fn();
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : `${name} failed`);
    } finally {
      setBusyAction("");
    }
  };

  const onSearch = async (event: FormEvent) => {
    event.preventDefault();
    await directory.search(query);
  };

  const onCreateGroup = async (event: FormEvent) => {
    event.preventDefault();
    const members = groupMembers.split(",").map((item) => item.trim()).filter(Boolean);
    await runAction("create-group", async () => {
      const sessionId = await directory.startGroupSession(groupName, members);
      void navigate({ to: "/chat/$sessionId", params: { sessionId } });
    });
  };

  if (auth.bootstrapping) {
    return (
      <WallpaperBackground>
        <div className="flex h-screen items-center justify-center text-white/45 text-sm">Loading...</div>
      </WallpaperBackground>
    );
  }

  return (
    <WallpaperBackground>
      <main className="mx-auto flex h-screen w-full max-w-[1400px] gap-6 p-4 md:p-6">
        <section className="w-full max-w-[380px] shrink-0">
          <GlassCard className="h-full w-full !p-0 flex flex-col" padding="0" cornerRadius={24} enableTilt={false}>
            <div className="px-5 pt-6 pb-4 border-b border-white/5 shrink-0">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <h1 className="text-2xl font-semibold text-white/90 tracking-tight">Contacts</h1>
                  <p className="mt-1 text-sm text-white/40">搜索用户、发起单聊或创建群聊。</p>
                </div>
                <button
                  className="rounded-full border border-white/10 px-3 py-1.5 text-xs text-white/70 hover:bg-white/5"
                  onClick={() => void navigate({ to: "/chat" })}
                  type="button"
                >
                  Back to Chat
                </button>
              </div>
            </div>

            <form className="px-5 pt-5" onSubmit={(event) => void onSearch(event)}>
              <GlassInput
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Search users"
              />
            </form>

            <div className="flex-1 overflow-y-auto p-5 space-y-3 custom-scrollbar">
              {directory.loading ? (
                <p className="text-sm text-white/40">Loading contacts...</p>
              ) : visibleResults.length === 0 ? (
                <p className="text-sm text-white/40">暂无联系人或搜索结果。</p>
              ) : (
                visibleResults.map((contact) => (
                  <div key={contact.username} className="rounded-[18px] border border-white/8 bg-white/[0.03] p-4">
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <p className="text-white/90 font-medium">{contact.nickname || contact.username}</p>
                        <p className="mt-1 text-sm text-white/45">@{contact.username}</p>
                      </div>
                      <button
                        className="rounded-full border border-white/10 px-3 py-1.5 text-xs text-white/75 hover:bg-white/5"
                        disabled={busyAction === `dm:${contact.username}`}
                        onClick={() => {
                          void runAction(`dm:${contact.username}`, async () => {
                            const sessionId = await directory.startDirectSession(contact.username);
                            void navigate({ to: "/chat/$sessionId", params: { sessionId } });
                          });
                        }}
                        type="button"
                      >
                        {busyAction === `dm:${contact.username}` ? "Opening..." : "Start Chat"}
                      </button>
                    </div>
                  </div>
                ))
              )}
            </div>
          </GlassCard>
        </section>

        <section className="flex-1 min-w-0">
          <GlassCard className="h-full w-full" padding="28px" cornerRadius={24} enableTilt={false}>
            <div className="max-w-2xl space-y-8">
              <header>
                <h2 className="text-xl font-semibold text-white/90">Create Group</h2>
                <p className="mt-2 text-sm text-white/45">
                  这是给 UI/UX 层继续包装的群聊创建骨架。成员以逗号分隔输入。
                </p>
              </header>

              <form className="space-y-4" onSubmit={(event) => void onCreateGroup(event)}>
                <GlassInput
                  value={groupName}
                  onChange={(event) => setGroupName(event.target.value)}
                  placeholder="Group name"
                />
                <GlassInput
                  value={groupMembers}
                  onChange={(event) => setGroupMembers(event.target.value)}
                  placeholder="Member usernames, comma separated"
                />
                <button
                  className="rounded-full border border-white/10 px-4 py-2 text-sm text-white/85 hover:bg-white/5"
                  disabled={busyAction === "create-group"}
                  type="submit"
                >
                  {busyAction === "create-group" ? "Creating..." : "Create Group"}
                </button>
              </form>

              <section className="space-y-3 text-sm text-white/50">
                <p>Current user: {auth.currentUser?.nickname || auth.currentUser?.username || "-"}</p>
                <p>Search status: {directory.searching ? "searching" : "idle"}</p>
                {directory.error ? <p className="text-red-400">directory: {directory.error}</p> : null}
                {actionError ? <p className="text-red-400">action: {actionError}</p> : null}
              </section>
            </div>
          </GlassCard>
        </section>
      </main>
    </WallpaperBackground>
  );
}
