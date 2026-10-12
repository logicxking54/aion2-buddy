using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using System.Text;
using System.Text.RegularExpressions;
using Newtonsoft.Json;
using UAssetAPI;
using UAssetAPI.ExportTypes;
using UAssetAPI.PropertyTypes.Objects;
using UAssetAPI.PropertyTypes.Structs;
using UAssetAPI.UnrealTypes;
using UAssetAPI.Unversioned;

public static class PlayerActionBuild
{
    const string Classes = "Sorcerer|Elementalist|Ranger|Fighter|Gladiator|Assassin";
    // Build 110: these inputs caused fatal UAssetAPI FSoftObjectPath stack overflows.
    // Keep them untouched; catch(Exception) cannot recover a StackOverflowException.
    static readonly HashSet<string> FatalParserInputs = new HashSet<string>(StringComparer.OrdinalIgnoreCase) {
        "Gladiator_Fly_Skill032_Fx", "Gladiator_Skill032_Fx"
    };
    static readonly Regex Context = new Regex(@"^/Game/(?:FX/)?Act/Player/(?:(?:\d+_)?(?:" + Classes + @")/|01_FX_Common/(?:02_Combat|04_Projectile)/(?:(?:SE|Proj)_)?(?:" + Classes + @")_)", RegexOptions.IgnoreCase);
    static bool KnownContext(string path) => Context.IsMatch(path) && !SelectionPolicy.HasGameplayReference(path);
    public static bool IsCandidate(string path) => !string.IsNullOrEmpty(path) && !path.Contains("..") && !path.Contains('\\') && !path.Contains(':') &&
        path.StartsWith("/Game/FX/Act/Player/", StringComparison.OrdinalIgnoreCase) && path.EndsWith("_Fx", StringComparison.OrdinalIgnoreCase) &&
        !FatalParserInputs.Contains(path.Substring(path.LastIndexOf('/') + 1)) && KnownContext(path);

    public static Dictionary<string, string> Exclusions(Dictionary<string, HashSet<string>> graph, IEnumerable<string> candidates)
    {
        var protectedPaths = SelectionPolicy.ProtectedClosure(graph);
        var extra = new Queue<string>(graph.Keys.Where(p => p.StartsWith("/ExternalPackage/", StringComparison.Ordinal) ||
            Regex.IsMatch(p, @"/(?:\d+_)(?:Cleric|Chanter|Templar)/|/Player/01_FX_Common/[^/]+/(?:(?:SE|Proj)_)?(?:Cleric|Chanter|Templar)_", RegexOptions.IgnoreCase)));
        while (extra.Count > 0)
        {
            string path = extra.Dequeue();
            if (!protectedPaths.Add(path)) continue;
            if (graph.TryGetValue(path, out var links)) foreach (string target in links) extra.Enqueue(target);
        }
        var selected = new HashSet<string>(candidates, StringComparer.OrdinalIgnoreCase);
        var known = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        var rejected = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        foreach (var node in graph)
            foreach (string target in node.Value)
                if (selected.Contains(target))
                {
                    if (KnownContext(node.Key)) known.Add(target);
                    else rejected[target] = "unknown-or-nonplayer-caller: " + node.Key;
                }
        foreach (string path in selected)
        {
            if (protectedPaths.Contains(path)) rejected[path] = "protected-transitive-reference";
            else if (!known.Contains(path)) rejected[path] = "no-confirmed-player-callsite";
        }
        return rejected;
    }

    static string Hash(string file) => Convert.ToHexString(SHA256.HashData(File.ReadAllBytes(file)));
    static bool Within(string path, string root) => path.Equals(root, StringComparison.OrdinalIgnoreCase) ||
        path.StartsWith(Path.EndsInDirectorySeparator(root) ? root : root + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase);
    public static void Build(string usmap, string manifestPath, string output, string report)
    {
        var manifest = JsonConvert.DeserializeObject<SelectionManifest>(File.ReadAllText(manifestPath));
        if (manifest == null || manifest.SchemaVersion != 1 || manifest.Kind != "player-fx-actions" || manifest.HeaderFailures.Count != 0 ||
            manifest.HeadersRead == 0 || manifest.StorePackages == 0) throw new InvalidDataException("Missing or incomplete player FX reference audit");
        string source = Path.TrimEndingDirectorySeparator(Path.GetFullPath(manifest.SourceRoot));
        output = Path.TrimEndingDirectorySeparator(Path.GetFullPath(output)); report = Path.GetFullPath(report);
        string scratch = output + "-checks";
        if (Directory.Exists(output) || Directory.Exists(scratch) || File.Exists(report) || Within(output, source) || Within(source, output) ||
            Within(report, source) || Within(report, output) || Within(report, scratch) || Within(scratch, source))
            throw new IOException("Output, checks, and report must be new and separate from the baseline and cook input");
        var blocked = new HashSet<string>(manifest.BlockedByReferences, StringComparer.OrdinalIgnoreCase);
        blocked.UnionWith(manifest.ActionExclusions.Keys);
        var unique = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (var item in manifest.Approved)
        {
            if (!IsCandidate(item.Package) || blocked.Contains(item.Package) || !unique.Add(item.Package)) throw new InvalidDataException("Invalid, protected, or duplicate action selection");
            string file = SelectionPolicy.SourceFile(source, item.Package);
            if (!Hash(file).Equals(item.UassetSha256, StringComparison.OrdinalIgnoreCase) ||
                !Hash(Path.ChangeExtension(file, ".uexp")).Equals(item.UexpSha256, StringComparison.OrdinalIgnoreCase))
                throw new InvalidDataException("Source changed after audit: " + item.Package);
        }
        var mappings = new Usmap(usmap);
        Directory.CreateDirectory(output); Directory.CreateDirectory(scratch);
        var results = new List<object>();
        int written = 0, totalTasks = 0;
        foreach (var item in manifest.Approved)
        {
            string input = SelectionPolicy.SourceFile(source, item.Package), destination = SelectionPolicy.SourceFile(output, item.Package);
            int tasks = 0; string reason = null;
            try
            {
                tasks = PatchAsset(input, destination, Path.Combine(scratch, results.Count.ToString("D5")), mappings);
                written++; totalTasks += tasks;
            }
            catch (Exception ex)
            {
                reason = ex.Message;
                if (File.Exists(destination)) File.Delete(destination);
                if (File.Exists(Path.ChangeExtension(destination, ".uexp"))) File.Delete(Path.ChangeExtension(destination, ".uexp"));
            }
            results.Add(new { item.Package, Status = reason == null ? "written" : "skipped", Reason = reason, DisabledTasks = tasks });
            if (results.Count % 25 == 0) GC.Collect();
            if (results.Count % 100 == 0) Console.WriteLine($"Actions {results.Count}/{manifest.Approved.Count}, written {written}, tasks {totalTasks}");
        }
        File.WriteAllText(report, JsonConvert.SerializeObject(new { SourceRoot = source, OutputRoot = output,
            AuditSha256 = Hash(manifestPath).ToLowerInvariant(), Approved = manifest.Approved.Count, Written = written, DisabledTasks = totalTasks,
            Skipped = results.Count - written, Results = results, Installed = false, InGameTested = false, manifest.Limitations }, Formatting.Indented), new UTF8Encoding(false));
        if (written == 0) throw new InvalidDataException("No FX actions passed; do not cook an empty output");
        Console.WriteLine($"Written {written} verified player FX actions; disabled {totalTasks} visual tasks; skipped {results.Count - written}");
    }
    static bool SamePair(string a, string b) => Hash(a) == Hash(b) && Hash(Path.ChangeExtension(a, ".uexp")) == Hash(Path.ChangeExtension(b, ".uexp"));
    static string Class(UAsset a, Export e) => e.ClassIndex.Index < 0 && -e.ClassIndex.Index <= a.Imports.Count
        ? a.Imports[-e.ClassIndex.Index - 1].ObjectName.ToString() : "";
    static UAsset LoadFxRoot(string input, Usmap mappings)
    {
        var asset = new UAsset(EngineVersion.VER_UE5_3, mappings,
            CustomSerializationFlags.SkipParsingExports | CustomSerializationFlags.SkipPreloadDependencyLoading);
        asset.FilePath = input;
        using var reader = asset.PathToReader(input);
        asset.Read(reader);
        var roots = asset.Exports.Where(e => e.OuterIndex.Index == 0).ToArray();
        if (roots.Length != 1 || Class(asset, roots[0]) != "AionAct") throw new InvalidDataException("Not a single AionAct root");
        // Task bodies contain unsupported schemas that can overflow the parser's stack.
        // Only root TaskLinks are needed; keep all other export bodies opaque.
        asset.ParseExport(reader, asset.Exports.IndexOf(roots[0]));
        if (asset.Exports.Count(e => e is NormalExport) != 1) throw new InvalidDataException("FX root could not be parsed");
        return asset;
    }
    static List<BoolPropertyData> VisualFlags(UAsset a)
    {
        var roots = a.Exports.Where(e => e.OuterIndex.Index == 0).ToArray();
        if (roots.Length != 1 || roots[0] is not NormalExport root || Class(a, root) != "AionAct") throw new InvalidDataException("Not a single parsed AionAct root");
        var kind = root.Data.SingleOrDefault(p => p.Name.ToString() == "ActAssetType") as EnumPropertyData;
        if (kind?.Value.ToString() != "Fx") throw new InvalidDataException("Not an FX-only action");
        var sections = root.Data.SingleOrDefault(p => p.Name.ToString() == "Sections") as ArrayPropertyData;
        if (sections == null) throw new InvalidDataException("Missing parsed FX sections");
        int rootIndex = a.Exports.IndexOf(root) + 1;
        var flags = new List<BoolPropertyData>();
        foreach (var sectionValue in sections.Value)
        {
            if (sectionValue is not StructPropertyData section) throw new InvalidDataException("Unknown section shape");
            var links = section.Value.SingleOrDefault(p => p.Name.ToString() == "TaskLinks") as ArrayPropertyData;
            if (links == null) throw new InvalidDataException("Missing parsed task links");
            foreach (var linkValue in links.Value)
            {
                if (linkValue is not StructPropertyData link) throw new InvalidDataException("Unknown task link shape");
                var reference = link.Value.SingleOrDefault(p => p.Name.ToString() == "TaskBase") as ObjectPropertyData;
                int index = reference?.Value.Index ?? 0;
                if (index <= 0 || index > a.Exports.Count || a.Exports[index - 1].OuterIndex.Index != rootIndex)
                    throw new InvalidDataException("Task reference is not local to the FX root");
                string taskClass = Class(a, a.Exports[index - 1]);
                if (taskClass != "AionActTask_Particle" && taskClass != "AionActTask_Streak") continue;
                var enabled = link.Value.SingleOrDefault(p => p.Name.ToString() == "bEnabled") as BoolPropertyData;
                if (enabled == null) throw new InvalidDataException("Unknown visual task enable flag");
                flags.Add(enabled);
            }
        }
        if (flags.Count == 0) throw new InvalidDataException("No supported visual tasks");
        return flags;
    }

    public static int PatchAsset(string input, string output, string scratch, Usmap mappings)
    {
        input = Path.GetFullPath(input); output = Path.GetFullPath(output); scratch = Path.TrimEndingDirectorySeparator(Path.GetFullPath(scratch));
        if (FatalParserInputs.Contains(Path.GetFileNameWithoutExtension(input))) throw new InvalidDataException("Quarantined fatal parser input; preserve this asset unchanged");
        if (File.Exists(output) || Directory.Exists(scratch) || input.Equals(output, StringComparison.OrdinalIgnoreCase) ||
            output.StartsWith(scratch + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase)) throw new IOException("Patch paths must be new and separate");
        if (File.Exists(Path.ChangeExtension(input, ".ubulk")) || File.Exists(Path.ChangeExtension(input, ".uptnl"))) throw new InvalidDataException("Unsupported bulk sidecar");
        var asset = LoadFxRoot(input, mappings);
        if (asset.GetNameMapIndexList().Any(n => n.ToString().StartsWith("/Game/") && SelectionPolicy.HasGameplayReference(n.ToString())))
            throw new InvalidDataException("Gameplay cue in referenced asset names");
        var flags = VisualFlags(asset);
        bool[] original = flags.Select(f => f.Value).ToArray();
        int count = original.Count(v => v);
        if (count == 0) throw new InvalidDataException("No active visual tasks");
        Directory.CreateDirectory(scratch);
        string roundtrip = Path.Combine(scratch, "original.uasset"), staged = Path.Combine(scratch, "patched.uasset"), restored = Path.Combine(scratch, "restored.uasset");
        asset.Write(roundtrip);
        if (!SamePair(input, roundtrip)) throw new InvalidDataException("Non-exact unmodified roundtrip");
        foreach (var flag in flags) flag.Value = false;
        asset.Write(staged);
        var verify = LoadFxRoot(staged, mappings);
        var verifyFlags = VisualFlags(verify);
        if (verifyFlags.Count != flags.Count || verifyFlags.Any(f => f.Value) || asset.Exports.Count != verify.Exports.Count)
            throw new InvalidDataException("Visual flag readback failed");
        for (int i = 0; i < asset.Exports.Count; i++)
            if (asset.Exports[i] is RawExport raw && (verify.Exports[i] is not RawExport other || !raw.Data.SequenceEqual(other.Data)))
                throw new InvalidDataException("Opaque task payload changed");
        // Prove the edit is reversible using only the allowed flags, not a separate rebuilt expected asset.
        for (int i = 0; i < verifyFlags.Count; i++) verifyFlags[i].Value = original[i];
        verify.Write(restored);
        if (!SamePair(input, restored)) throw new InvalidDataException("Nonvisual data changed");
        Directory.CreateDirectory(Path.GetDirectoryName(output));
        File.Copy(staged, output);
        File.Copy(Path.ChangeExtension(staged, ".uexp"), Path.ChangeExtension(output, ".uexp"));
        if (!SamePair(staged, output) || !SamePair(input, roundtrip)) throw new InvalidDataException("Published patch or source bytes changed");
        return count;
    }
}
