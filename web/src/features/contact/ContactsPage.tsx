import { FormEvent, useMemo, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { GlassCard } from "../../components/GlassCard";
import { WallpaperBackground } from "../../components/WallpaperBackground";
import { useAuthGuard } from "../../hooks/useAuthGuard";
import { useContactDirectory } from "../../hooks/useContactDirectory";
import { Users, Search, Plus, ArrowLeft } from "lucide-react";

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
        <div className="flex h-screen items-center justify-center text-white/50 text-sm">Loading...</div>
      </WallpaperBackground>
    );
  }

  return (
    <WallpaperBackground>
      <main className="mx-auto flex h-screen w-full max-w-[1400px] gap-4 md:gap-6 p-4 md:p-6 relative z-10">
        <section className="w-full max-w-[380px] shrink-0">
          <GlassCard className="h-full w-full !p-0 flex flex-col relative overflow-hidden" padding="0" cornerRadius={24} enableTilt={false}>
            <div className="px-5 pt-6 pb-4 border-b border-white/10 shrink-0 bg-white/[0.02] relative z-10">
              <div className="flex items-center justify-between gap-3 mb-5">
                <div>
                  <h1 className="text-[22px] font-semibold text-white tracking-tight flex items-center gap-2">
                    <Users className="w-5 h-5 text-white/80" />
                    Contacts
                  </h1>
                </div>
                <button
                  className="rounded-full bg-white/5 border border-white/10 w-9 h-9 flex items-center justify-center text-white hover:bg-white/10 transition-colors shadow-sm"
                  onClick={() => void navigate({ to: "/chat" })}
                  title="Back to Chat"
                  type="button"
                >
                  <ArrowLeft className="w-4 h-4" />
                </button>
              </div>

              <form onSubmit={(event) => void onSearch(event)}>
                <label className="flex items-center gap-3 rounded-[16px] border border-white/15 bg-black/20 px-4 py-3 text-white/60 focus-within:border-white/30 focus-within:text-white focus-within:bg-black/30 transition-all shadow-inner">
                  <Search className="w-[18px] h-[18px] shrink-0" />
                  <input
                    className="bg-transparent outline-none text-[15px] w-full text-white placeholder:text-white/40"
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder="Search users"
                    value={query}
                  />
                </label>
              </form>
            </div>

            <div className="flex-1 overflow-y-auto p-4 space-y-2.5 custom-scrollbar relative z-10">
              {directory.loading ? (
                <div className="text-center mt-10">
                  <p className="text-[14px] text-white/50">Loading contacts...</p>
                </div>
              ) : visibleResults.length === 0 ? (
                <div className="text-center mt-10">
                  <p className="text-[14px] text-white/50">No contacts or search results found.</p>
                </div>
              ) : (
                visibleResults.map((contact) => (
                  <div key={contact.username} className="rounded-[18px] border border-white/10 bg-white/[0.03] p-4 hover:bg-white/[0.06] transition-colors shadow-sm backdrop-blur-md">
                    <div className="flex items-center justify-between gap-3">
                      <div className="flex items-center gap-3">
                        <div className="w-[42px] h-[42px] rounded-full bg-gradient-to-br from-blue-400/20 to-purple-400/20 border border-white/10 flex items-center justify-center text-white font-medium shadow-[inset_0_1px_2px_rgba(255,255,255,0.2)]">
                          {(contact.nickname || contact.username).charAt(0).toUpperCase()}
                        </div>
                        <div>
                          <p className="text-white font-medium text-[15px]">{contact.nickname || contact.username}</p>
                          <p className="text-[13px] text-white/50">@{contact.username}</p>
                        </div>
                      </div>
                      <button
                        className="rounded-full border border-[var(--glass-btn-primary-border)] bg-[var(--color-primary)] px-4 py-2 text-[13px] font-medium text-white hover:bg-[var(--color-primary-hover)] transition-colors shadow-sm disabled:opacity-50"
                        disabled={busyAction === `dm:${contact.username}`}
                        onClick={() => {
                          void runAction(`dm:${contact.username}`, async () => {
                            const sessionId = await directory.startDirectSession(contact.username);
                            void navigate({ to: "/chat/$sessionId", params: { sessionId } });
                          });
                        }}
                        type="button"
                      >
                        {busyAction === `dm:${contact.username}` ? "..." : "Chat"}
                      </button>
                    </div>
                  </div>
                ))
              )}
            </div>
          </GlassCard>
        </section>

        <section className="flex-1 min-w-0">
          <GlassCard className="h-full w-full relative overflow-hidden" padding="40px" cornerRadius={24} enableTilt={false}>
            <div className="max-w-xl relative z-10">
              <header className="mb-8">
                <h2 className="text-[24px] font-semibold text-white tracking-tight flex items-center gap-2">
                  <Plus className="w-5 h-5 text-[var(--color-primary)]" />
                  Create Group
                </h2>
                <p className="mt-2 text-[15px] text-white/60">
                  这是给 UI/UX 层继续包装的群聊创建骨架。成员以逗号分隔输入。
                </p>
              </header>

              <form className="space-y-5 bg-white/[0.02] border border-white/10 p-6 rounded-[24px] shadow-sm backdrop-blur-md" onSubmit={(event) => void onCreateGroup(event)}>
                <div className="space-y-1.5">
                  <label className="text-[12px] uppercase tracking-[0.1em] font-medium text-white/50 ml-1">Group Name</label>
                  <div className="flex items-center gap-3 rounded-[16px] border border-white/15 bg-black/20 px-4 py-3 text-white focus-within:border-white/30 focus-within:bg-black/30 transition-all shadow-inner">
                    <input
                      className="bg-transparent outline-none text-[15px] w-full text-white placeholder:text-white/30"
                      value={groupName}
                      onChange={(event) => setGroupName(event.target.value)}
                      placeholder="e.g. Design Team"
                    />
                  </div>
                </div>

                <div className="space-y-1.5">
                  <label className="text-[12px] uppercase tracking-[0.1em] font-medium text-white/50 ml-1">Members</label>
                  <div className="flex items-center gap-3 rounded-[16px] border border-white/15 bg-black/20 px-4 py-3 text-white focus-within:border-white/30 focus-within:bg-black/30 transition-all shadow-inner">
                    <input
                      className="bg-transparent outline-none text-[15px] w-full text-white placeholder:text-white/30"
                      value={groupMembers}
                      onChange={(event) => setGroupMembers(event.target.value)}
                      placeholder="username1, username2..."
                    />
                  </div>
                </div>
                
                <div className="pt-2">
                  <button
                    className="w-full rounded-full border border-[var(--glass-btn-primary-border)] bg-[var(--color-primary)] px-5 py-3 text-[15px] font-medium text-white hover:bg-[var(--color-primary-hover)] transition-all shadow-[0_4px_12px_rgba(180,83,60,0.25),inset_0_1px_1px_rgba(255,255,255,0.3)] disabled:opacity-50"
                    disabled={busyAction === "create-group" || !groupName.trim()}
                    type="submit"
                  >
                    {busyAction === "create-group" ? "Creating..." : "Create Group"}
                  </button>
                </div>
              </form>

              <section className="mt-8 space-y-2 text-[13px] text-white/40 p-4 border border-white/5 rounded-[16px] bg-black/10">
                <p>Current user: {auth.currentUser?.nickname || auth.currentUser?.username || "-"}</p>
                <p>Search status: {directory.searching ? "searching" : "idle"}</p>
                {directory.error ? <p className="text-red-400/80">directory error: {directory.error}</p> : null}
                {actionError ? <p className="text-red-400/80">action error: {actionError}</p> : null}
              </section>
            </div>
          </GlassCard>
        </section>
      </main>
    </WallpaperBackground>
  );
}
