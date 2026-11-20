import { useState } from 'react';
import { DebugPanel } from './features/debug/DebugPanel';
import { EntityDetail } from './features/detail/EntityDetail';
import { FilterBar } from './features/filters/FilterBar';
import { EntityList } from './features/list/EntityList';
import { WorldMap } from './features/map/WorldMap';
import { StateOverlay } from './features/status/StateOverlay';
import { StatusBar } from './features/status/StatusBar';
import { TargetPicker } from './features/targets/TargetPicker';
import { LoginGate } from './features/auth/LoginGate';
import { AuthProvider } from './hooks/useAuth';
import { InspectorProvider } from './hooks/useInspector';

export function App() {
  const [debugOpen, setDebugOpen] = useState(false);
  return (
    <AuthProvider>
      <LoginGate>
        <InspectorProvider>
          <div className="app-shell">
            <header className="app-header">
              <div className="brand">
                <p className="brand-eyebrow">Golem Engine</p>
                <h1 className="brand-title">Inspector</h1>
              </div>
              <TargetPicker />
              <FilterBar />
            </header>
            <div className="app-main">
              <EntityList />
              <div className="map-stage">
                <WorldMap />
                <StateOverlay />
              </div>
              <EntityDetail />
            </div>
            {debugOpen ? <DebugPanel /> : null}
            <StatusBar debugOpen={debugOpen} onToggleDebug={() => setDebugOpen((o) => !o)} />
          </div>
        </InspectorProvider>
      </LoginGate>
    </AuthProvider>
  );
}
