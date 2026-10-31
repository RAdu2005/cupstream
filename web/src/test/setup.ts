import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';
import { realtime } from '@/lib/realtime';
import { resetCapacity } from '@/lib/useBandwidth';
import { FakeWebSocket } from './fakeWebSocket';

afterEach(() => {
  cleanup();
  realtime.reset();
  resetCapacity();
  FakeWebSocket.instances = [];
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
