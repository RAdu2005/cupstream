import { create } from 'zustand';

type UIState = {
  theatre: boolean;
  chatOpen: boolean;
  toggleTheatre: () => void;
  toggleChat: () => void;
  setChat: (open: boolean) => void;
};

export const useUI = create<UIState>((set) => ({
  theatre: false,
  chatOpen: true,
  toggleTheatre: () => set((s) => ({ theatre: !s.theatre })),
  toggleChat: () => set((s) => ({ chatOpen: !s.chatOpen })),
  setChat: (open) => set({ chatOpen: open }),
}));
