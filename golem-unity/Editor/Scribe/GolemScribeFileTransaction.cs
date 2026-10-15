using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;

namespace GolemEngine.Unity.Editor
{
    internal enum ScribeMutationStage { BeforeWrite, BeforeReplace, AfterWrite, BeforeDelete, AfterDelete, BeforeRollback }

    // Per-file atomic replacement with an in-memory journal, not a crash-recovery protocol.
    internal sealed class GolemScribeFileTransaction
    {
        internal static Action<ScribeMutationStage, string> MutationHookForTests;
        private static readonly Encoding Utf8NoBom = new UTF8Encoding(false);
        private readonly List<Mutation> _journal = new List<Mutation>();

        private sealed class Mutation
        {
            internal string Path;
            internal byte[] Before;
            internal byte[] After;
            internal bool Applied;
        }

        internal bool WriteManaged(string path, string content)
        {
            MutationHookForTests?.Invoke(ScribeMutationStage.BeforeWrite, path);
            var existing = ReadManaged(path);
            if (existing != null && string.Equals(Decode(existing), content, StringComparison.Ordinal)) return false;
            var bytes = Utf8NoBom.GetBytes(content ?? string.Empty);
            try
            {
                WriteAtomic(path, bytes, () =>
                {
                    MutationHookForTests?.Invoke(ScribeMutationStage.BeforeReplace, path);
                    // Authorization is checked after staging, immediately before the filesystem operation.
                    var before = ReadManaged(path);
                    var mutation = new Mutation { Path = path, Before = before, After = bytes };
                    _journal.Add(mutation);
                    return mutation;
                });
                MutationHookForTests?.Invoke(ScribeMutationStage.AfterWrite, path);
                return true;
            }
            catch (Exception ex)
            {
                throw new IOException($"Failed to write '{path}': {ex.Message}", ex);
            }
        }

        internal void DeleteManaged(string path)
        {
            try
            {
                MutationHookForTests?.Invoke(ScribeMutationStage.BeforeDelete, path);
                var before = ReadManaged(path);
                if (before == null) throw new FileNotFoundException("Planned orphan no longer exists.", path);
                var mutation = new Mutation { Path = path, Before = before };
                _journal.Add(mutation);
                File.Delete(path);
                mutation.Applied = true;
                MutationHookForTests?.Invoke(ScribeMutationStage.AfterDelete, path);
            }
            catch (Exception ex)
            {
                throw new IOException($"Failed to delete '{path}': {ex.Message}", ex);
            }
        }

        internal IReadOnlyList<string> Rollback()
        {
            var errors = new List<string>();
            for (var i = _journal.Count - 1; i >= 0; i--)
            {
                var mutation = _journal[i];
                if (!mutation.Applied) continue;
                try
                {
                    MutationHookForTests?.Invoke(ScribeMutationStage.BeforeRollback, mutation.Path);
                    var current = ReadBytes(mutation.Path);
                    if (current != null && !GolemYamlWriter.IsScribeOwned(Decode(current)) &&
                        (mutation.After == null || !current.SequenceEqual(mutation.After)))
                    {
                        throw new IOException("Refusing to overwrite a handwritten file introduced during the transaction.");
                    }
                    if (mutation.Before == null)
                    {
                        if (current != null) File.Delete(mutation.Path);
                    }
                    else
                    {
                        WriteAtomic(mutation.Path, mutation.Before,
                            () => new Mutation { Before = current });
                    }
                }
                catch (Exception ex)
                {
                    errors.Add($"Failed to roll back '{mutation.Path}': {ex.Message}");
                }
            }
            if (errors.Count > 0)
                errors.Add("Scribe rollback is incomplete; files may remain changed. Inspect the reported paths before exporting again.");
            return errors;
        }

        private static byte[] ReadManaged(string path)
        {
            var bytes = ReadBytes(path);
            if (bytes != null && !GolemYamlWriter.IsScribeOwned(Decode(bytes)))
                throw new IOException($"Refusing to overwrite or delete handwritten file '{path}'.");
            return bytes;
        }

        private static byte[] ReadBytes(string path)
        {
            try { return File.ReadAllBytes(path); }
            catch (FileNotFoundException) { return null; }
            catch (DirectoryNotFoundException) { return null; }
            catch (Exception ex) { throw new IOException($"Failed to read '{path}': {ex.Message}", ex); }
        }

        private static string Decode(byte[] bytes)
        {
            using (var stream = new MemoryStream(bytes))
            using (var reader = new StreamReader(stream, Encoding.UTF8, detectEncodingFromByteOrderMarks: true))
                return reader.ReadToEnd();
        }

        private static void WriteAtomic(string path, byte[] bytes, Func<Mutation> beforeCommit)
        {
            var directory = Path.GetDirectoryName(path);
            if (!string.IsNullOrEmpty(directory)) Directory.CreateDirectory(directory);
            var temporary = path + "." + Guid.NewGuid().ToString("N") + ".tmp";
            Exception failure = null;
            try
            {
                File.WriteAllBytes(temporary, bytes);
                var mutation = beforeCommit();
                if (mutation.Before != null)
                    File.Replace(temporary, path, null);
                else
                    File.Move(temporary, path);
                mutation.Applied = true;
            }
            catch (Exception ex)
            {
                failure = ex;
                throw;
            }
            finally
            {
                try { File.Delete(temporary); }
                catch (Exception cleanupError)
                {
                    var message = $"Failed to remove temporary file '{temporary}': {cleanupError.Message}";
                    if (failure != null) throw new AggregateException(failure.Message + "; " + message, failure, cleanupError);
                    throw new IOException(message, cleanupError);
                }
            }
        }
    }
}
