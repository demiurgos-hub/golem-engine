import { useState, type ReactNode } from 'react';
import { formatScalar, type DecodedField, type Value } from '../../protocol/decode';
import { FieldKind, type SchemaField } from '../../protocol/messages';

function typeLabel(field: SchemaField): string {
  switch (field.kind) {
    case FieldKind.List:
      return `list<${field.type}>`;
    case FieldKind.Map:
      return `dict<${field.keyType}, ${field.type}>`;
    default:
      return field.type;
  }
}

function summary(v: Value): string {
  switch (v.kind) {
    case 'scalar':
      return formatScalar(v.type, v.value);
    case 'list':
      return `[${v.items.length}]`;
    case 'map':
      return `{${v.entries.length}}`;
    case 'message': {
      const parts = v.fields.filter((f) => f.present && f.value.kind === 'scalar').slice(0, 3).map((f) => `${f.field.name}: ${summary(f.value)}`);
      return parts.length ? `{ ${parts.join(', ')}${v.fields.length > parts.length ? ', …' : ''} }` : '{}';
    }
    case 'error':
      return `⚠ ${v.message}`;
  }
}

function expandable(v: Value): boolean {
  return (v.kind === 'list' && v.items.length > 0) || (v.kind === 'map' && v.entries.length > 0) || v.kind === 'message';
}

function Node({ name, type, value, depth, badges, dim }: { name: string; type: string; value: Value; depth: number; badges?: ReactNode; dim?: boolean }) {
  const canOpen = expandable(value);
  const [open, setOpen] = useState(depth < 1);
  let children: ReactNode = null;
  if (canOpen && open) {
    if (value.kind === 'list') {
      children = value.items.map((item, i) => <Node key={i} name={`[${i}]`} type={value.type} value={item} depth={depth + 1} />);
    } else if (value.kind === 'map') {
      children = value.entries.map((e, i) => (
        <Node key={i} name={e.key.kind === 'scalar' ? formatScalar(e.key.type, e.key.value) : `#${i}`} type={value.type} value={e.value} depth={depth + 1} />
      ));
    } else if (value.kind === 'message') {
      children = value.fields.map((f) => <Node key={f.field.tag} name={f.field.name} type={typeLabel(f.field)} value={f.value} depth={depth + 1} dim={!f.present} />);
    }
  }
  return (
    <div className="tv-node" style={{ paddingLeft: depth * 12 }}>
      <button type="button" className={`tv-row${canOpen ? ' tv-row--expandable' : ''}${dim ? ' tv-row--default' : ''}`} onClick={() => canOpen && setOpen((o) => !o)} disabled={!canOpen}>
        <span className="tv-twist">{canOpen ? (open ? '▾' : '▸') : '·'}</span>
        <span className="tv-name">
          {name}
          {badges}
        </span>
        <span className="tv-type">{type}</span>
        {!canOpen || !open ? <span className={`tv-value${value.kind === 'error' ? ' danger' : ''}`}>{summary(value)}</span> : null}
      </button>
      {children ? <div className="tv-children">{children}</div> : null}
    </div>
  );
}

function FieldBadges({ field }: { field: SchemaField }) {
  return (
    <>
      {field.sync === 'once' ? <span className="field-badge" title="sync: once — set at spawn, never sent in deltas">once</span> : null}
      {field.ownerOnly ? (
        <span className="field-badge field-badge--owner" title="visibility: owner — replicated only to the owning session; the inspector sees it">
          owner
        </span>
      ) : null}
    </>
  );
}

/** Renders decoded schema fields; absent fields show their default, dimmed. */
export function FieldTree({ fields }: { fields: DecodedField[] }) {
  if (fields.length === 0) return <p className="muted">No fields</p>;
  return (
    <div className="tv-tree">
      {fields.map((f) => (
        <Node key={f.field.tag} name={f.field.name} type={typeLabel(f.field)} value={f.value} depth={0} badges={<FieldBadges field={f.field} />} dim={!f.present} />
      ))}
    </div>
  );
}
