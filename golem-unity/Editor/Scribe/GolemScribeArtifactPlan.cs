using System;
using System.Collections.Generic;
using System.Linq;

namespace GolemEngine.Unity.Editor
{
    // Pure preparation and classification. Neither a snapshot nor a proposed operation authorizes IO.
    internal sealed class GolemScribeArtifactPlan
    {
        internal sealed class Artifact
        {
            internal string Kind { get; }
            internal string SourceGuid { get; }
            internal string Entity { get; }
            internal string Path { get; }
            internal string Hash { get; }
            internal string Content { get; }

            internal Artifact(GolemScribeArtifactRecord record, string path, bool desired)
            {
                Kind = record.Kind;
                SourceGuid = record.SourceGuid;
                Entity = record.Entity;
                Path = path;
                Content = desired ? record.PendingContent : null;
                Hash = desired ? GolemScribeManifest.ComputeContentHash(Content) : record.Hash;
            }

            internal GolemScribeArtifactRecord ToManifestRecord() => new GolemScribeArtifactRecord
            {
                Kind = Kind, SourceGuid = SourceGuid, Entity = Entity, Path = Path, Hash = Hash
            };
        }

        internal sealed class Issue
        {
            internal string WriterError { get; }
            internal string ValidationError { get; }
            internal Issue(string writerError, string validationError)
            {
                WriterError = writerError;
                ValidationError = validationError;
            }
        }

        internal sealed class Inputs
        {
            internal string Root { get; }
            internal IReadOnlyList<Artifact> Desired { get; }
            internal IReadOnlyList<Artifact> Previous { get; }
            internal IReadOnlyList<Artifact> Preserved { get; }
            internal IReadOnlyList<Issue> Issues { get; }
            internal IReadOnlyList<string> Paths { get; }

            internal Inputs(string root, List<Artifact> desired, List<Artifact> previous,
                List<Artifact> preserved, List<Issue> issues)
            {
                Root = root;
                Desired = desired.AsReadOnly();
                Previous = previous.AsReadOnly();
                Preserved = preserved.AsReadOnly();
                Issues = issues.AsReadOnly();
                Paths = desired.Concat(previous).Select(r => r.Path).Distinct(StringComparer.Ordinal).ToList().AsReadOnly();
            }
        }

        internal enum Operation { None, Write, Delete, Blocked }

        internal sealed class Entry
        {
            internal Artifact Desired { get; }
            internal Artifact Previous { get; }
            internal ScribeArtifactSnapshot Disk { get; }
            internal string Path => Desired?.Path ?? Previous.Path;
            internal string Kind => Desired?.Kind ?? Previous.Kind;
            internal bool ContentMatches => Desired != null && string.Equals(Disk.Content, Desired.Content, StringComparison.Ordinal);
            internal bool PriorHashMatches => Previous != null && string.Equals(Previous.Hash, Disk.Hash, StringComparison.Ordinal);
            internal bool HasReadError => Disk.Status == ScribeArtifactStatus.ReadFailure || Disk.Status == ScribeArtifactStatus.InvalidPath;
            internal bool Missing => Disk.Status == ScribeArtifactStatus.Missing;
            internal bool ManuallyModified => Desired != null &&
                (Disk.Status == ScribeArtifactStatus.Handwritten ||
                 (Disk.Status == ScribeArtifactStatus.Owned && !string.IsNullOrEmpty(Previous?.Hash) && !PriorHashMatches));
            internal bool MissingManifest => Desired != null && Previous == null && Disk.Status == ScribeArtifactStatus.Owned && ContentMatches;
            internal bool Stale => Desired != null && Disk.Status == ScribeArtifactStatus.Owned &&
                ((!ContentMatches && !ManuallyModified) || MissingManifest);
            internal bool Orphaned => Desired == null && Disk.Status == ScribeArtifactStatus.Owned;
            internal Operation ProposedOperation => HasReadError ? Operation.Blocked : Desired != null
                ? Disk.Status == ScribeArtifactStatus.Handwritten ? Operation.Blocked
                    : Missing || !ContentMatches ? Operation.Write : Operation.None
                : Orphaned ? Operation.Delete : Operation.None;

            internal Entry(Artifact desired, Artifact previous, ScribeArtifactSnapshot disk)
            {
                Desired = desired;
                Previous = previous;
                Disk = disk;
            }
        }

        internal Inputs Prepared { get; }
        internal IReadOnlyList<Entry> Entries { get; }
        internal IReadOnlyList<Entry> Writes { get; }
        internal IReadOnlyList<Entry> Deletions { get; }
        internal IReadOnlyList<Artifact> ManifestRecords { get; }

        private GolemScribeArtifactPlan(Inputs prepared, List<Entry> entries, List<Entry> deletions)
        {
            Prepared = prepared;
            Entries = entries.AsReadOnly();
            Writes = entries.Where(e => e.Desired != null).OrderBy(e => e.Path, StringComparer.Ordinal).ToList().AsReadOnly();
            Deletions = deletions.OrderBy(e => e.Path, StringComparer.Ordinal).ToList().AsReadOnly();
            ManifestRecords = prepared.Preserved.Concat(prepared.Desired)
                .OrderBy(r => r.Path, StringComparer.Ordinal).ToList().AsReadOnly();
        }

        internal static Inputs Prepare(string rootFull, IReadOnlyCollection<string> kinds,
            IReadOnlyList<GolemScribeArtifactRecord> previous, IReadOnlyList<GolemScribeArtifactRecord> desired)
        {
            var kindSet = new HashSet<string>((kinds ?? Array.Empty<string>()).Where(k => !string.IsNullOrEmpty(k)), StringComparer.Ordinal);
            var desiredRecords = new List<Artifact>();
            var priorRecords = new List<Artifact>();
            var preserved = new List<Artifact>();
            var issues = new List<Issue>();
            var desiredPaths = new HashSet<string>(StringComparer.Ordinal);
            foreach (var record in desired ?? Array.Empty<GolemScribeArtifactRecord>())
            {
                if (record == null || string.IsNullOrEmpty(record.Path))
                {
                    issues.Add(new Issue("Scribe artifact is missing a relative path.", "Desired Scribe artifact is missing a relative path."));
                }
                else if (string.IsNullOrEmpty(record.Kind) || !kindSet.Contains(record.Kind))
                {
                    issues.Add(new Issue(
                        $"Scribe artifact '{record.Path}' has kind '{record.Kind}', expected one of: {string.Join(", ", kindSet.OrderBy(k => k, StringComparer.Ordinal))}.",
                        $"Desired Scribe artifact '{record.Path}' has unexpected kind '{record.Kind}'."));
                }
                else if (record.PendingContent == null)
                {
                    issues.Add(new Issue($"Scribe artifact '{record.Path}' is missing generated content.",
                        $"Desired Scribe artifact '{record.Path}' is missing generated content."));
                }
                else if (!GolemScribePaths.TryResolveRelative(rootFull, record.Path, out _, out var normalized, out var error))
                {
                    issues.Add(new Issue(error, error));
                }
                else if (!desiredPaths.Add(normalized))
                {
                    issues.Add(new Issue($"Duplicate Scribe output path '{normalized}'.", $"Duplicate desired Scribe output path '{normalized}'."));
                }
                else
                {
                    desiredRecords.Add(new Artifact(record, normalized, desired: true));
                }
            }

            foreach (var prior in previous ?? Array.Empty<GolemScribeArtifactRecord>())
            {
                if (prior == null) continue;
                var selected = !string.IsNullOrEmpty(prior.Kind) && kindSet.Contains(prior.Kind);
                if (string.IsNullOrEmpty(prior.Path))
                {
                    issues.Add(new Issue(selected ? "Prior Scribe manifest record is missing a path."
                        : "Preserved manifest record has hostile or corrupt path: missing path", null));
                }
                else if (!GolemScribePaths.TryResolveRelative(rootFull, prior.Path, out _, out var normalized, out var error))
                {
                    issues.Add(new Issue((selected ? "Hostile or corrupt manifest path rejected: "
                        : "Preserved manifest record has hostile or corrupt path: ") + error,
                        selected ? "Manifest ownership path rejected: " + error : null));
                }
                else
                {
                    (selected ? priorRecords : preserved).Add(new Artifact(prior, normalized, desired: false));
                }
            }
            return new Inputs(rootFull, desiredRecords, priorRecords, preserved, issues);
        }

        internal static GolemScribeArtifactPlan Build(Inputs inputs, IReadOnlyDictionary<string, ScribeArtifactSnapshot> snapshots)
        {
            var priorByPath = new Dictionary<string, Artifact>(StringComparer.Ordinal);
            foreach (var prior in inputs.Previous) priorByPath[prior.Path] = prior;
            var entries = new List<Entry>();
            var desiredPaths = new HashSet<string>(StringComparer.Ordinal);
            foreach (var desired in inputs.Desired)
            {
                desiredPaths.Add(desired.Path);
                priorByPath.TryGetValue(desired.Path, out var prior);
                entries.Add(new Entry(desired, prior, snapshots[desired.Path]));
            }
            foreach (var prior in priorByPath.Values.OrderBy(r => r.Path, StringComparer.Ordinal))
            {
                if (!desiredPaths.Contains(prior.Path)) entries.Add(new Entry(null, prior, snapshots[prior.Path]));
            }
            // Keep the writer's prior-record sequence, including corrupt duplicates. Validation owns
            // duplicate-manifest diagnostics; planning must not silently repair that input for export.
            var deletions = inputs.Previous.Where(p => !desiredPaths.Contains(p.Path))
                .Select(p => new Entry(null, p, snapshots[p.Path])).Where(e => e.Orphaned).ToList();
            return new GolemScribeArtifactPlan(inputs, entries, deletions);
        }
    }
}
