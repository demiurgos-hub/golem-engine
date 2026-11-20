import { useMemo } from 'react';
import { useEntityStore, useInspector, useSelectedEntity } from '../../hooks/useInspector';
import { typeColor } from '../../lib/entityTypes';
import { decodeEntity, readVarintField } from '../../protocol/decode';
import { entityByTag } from '../../protocol/messages';
import { FieldTree } from './ValueTree';

export function EntityDetail() {
  const entity = useSelectedEntity();
  const { schema } = useEntityStore();
  const { spaces } = useInspector();
  const schemaEntity = entity ? entityByTag(schema, entity.typeTag) : undefined;
  // Only the selected entity is fully decoded, and only when it changes.
  const decoded = useMemo(
    () => (entity && schema && schemaEntity ? decodeEntity(schema, schemaEntity, entity.fields) : null),
    [entity, schema, schemaEntity],
  );

  if (!entity) {
    return (
      <aside className="panel detail-panel">
        <header className="panel-header">
          <h2>Entity</h2>
        </header>
        <div className="panel-body empty-state">
          <p className="muted">Select an entity on the map or in the list.</p>
        </div>
      </aside>
    );
  }

  const color = typeColor(entity.typeName);
  const variables = decoded?.fields.filter((f) => f.field.role === 'variable') ?? [];
  const revision = readVarintField(entity.fields, 1000);

  return (
    <aside className="panel detail-panel">
      <header className="panel-header">
        <h2>Entity</h2>
        <span className="type-chip" style={{ borderColor: color, color }}>
          {entity.typeName}
        </span>
      </header>
      <div className="panel-body detail-scroll">
        <section className="detail-section">
          <h3>Identity</h3>
          <dl className="detail-dl">
            <div>
              <dt>ID</dt>
              <dd className="mono">{entity.id}</dd>
            </div>
            <div>
              <dt>Type</dt>
              <dd>
                {entity.typeName} <span className="muted">(tag {entity.typeTag})</span>
                {schemaEntity?.global ? <span className="field-badge">global</span> : null}
              </dd>
            </div>
            {spaces.length > 1 || entity.space !== 0 ? (
              <div>
                <dt>Space</dt>
                <dd className="mono">{entity.space}</dd>
              </div>
            ) : null}
            <div>
              <dt>Revision</dt>
              <dd className="mono">{revision.toString()}</dd>
            </div>
          </dl>
        </section>
        <section className="detail-section">
          <h3>Position</h3>
          <dl className="detail-dl">
            <div>
              <dt>X</dt>
              <dd className="mono">{entity.position.x.toFixed(3)}</dd>
            </div>
            <div>
              <dt>Y</dt>
              <dd className="mono">{entity.position.y.toFixed(3)}</dd>
            </div>
            {schema?.dimensions === 3 || schemaEntity?.is3d ? (
              <div>
                <dt>Z</dt>
                <dd className="mono">{entity.position.z.toFixed(3)}</dd>
              </div>
            ) : null}
          </dl>
        </section>
        <section className="detail-section">
          <h3>Fields</h3>
          {decoded ? <FieldTree fields={variables} /> : <p className="muted">Schema not received yet.</p>}
          {decoded && decoded.unknown.length > 0 ? (
            <p className="muted comp-names">
              Unknown tags: {decoded.unknown.map((u) => `${u.tag} (${u.bytes} B)`).join(', ')}
            </p>
          ) : null}
        </section>
      </div>
    </aside>
  );
}
