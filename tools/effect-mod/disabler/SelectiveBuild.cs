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

public sealed class SelectedAsset
{
    public string Package;
    public string UassetSha256;
    public string UexpSha256;
}
public sealed class SelectionManifest
{
    public int SchemaVersion = 1;
    public string Kind = "niagara";
    public string SourceRoot;
    public string StoreSha256;
    public int StorePackages;
    public int HeadersRead;
    public List<string> HeaderFailures = new List<string>();
    public List<SelectedAsset> Approved = new List<SelectedAsset>();
    public List<string> BlockedByReferences = new List<string>();
    public Dictionary<string, string> ActionExclusions = new Dictionary<string, string>();
    public string Limitations = "Package-store hard imports and header /Game name references only. Not a complete dynamic-reference or visual-mechanic proof.";
}
public static class SelectiveBuild
{
    static string Hash(string path) => Convert.ToHexString(SHA256.HashData(File.ReadAllBytes(path))).ToLowerInvariant();
    static void JsonFile(string path, object value) => File.WriteAllText(path, JsonConvert.SerializeObject(value, Formatting.Indented), new UTF8Encoding(false));
    static string DirectoryPath(string path) => Path.TrimEndingDirectorySeparator(Path.GetFullPath(path));
    static bool Within(string path, string directory) => path.Equals(directory, StringComparison.OrdinalIgnoreCase) ||
        path.StartsWith(Path.EndsInDirectorySeparator(directory) ? directory : directory + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase);
    public static int Run(string[] args)
    {
        try
        {
            if (args[0] == "--audit" && args.Length == 4) { Audit(args[1], args[2], args[3]); return 0; }
            if (args[0] == "--audit-actions" && args.Length == 4) { Audit(args[1], args[2], args[3], true); return 0; }
            if (args[0] == "--player-actions" && args.Length == 5) { PlayerActionBuild.Build(args[1], args[2], args[3], args[4]); return 0; }
            if (args[0] == "--selective" && args.Length == 5) { Build(args[1], args[2], args[3], args[4]); return 0; }
            Console.Error.WriteLine("--audit <extracted-root> <package-store.txt> <manifest.json> OR --selective <usmap> <manifest.json> <new-output-root> <report.json>");
            return 2;
        }
        catch (Exception ex) { Console.Error.WriteLine(ex.Message); return 2; }
    }
    static void Audit(string root, string store, string output, bool actions = false)
    {
        root = DirectoryPath(root);
        if (File.Exists(output)) throw new IOException("Audit output already exists");
        var manifest = new SelectionManifest { SourceRoot = root, StoreSha256 = Hash(store), Kind = actions ? "player-fx-actions" : "niagara" };
        var ids = new Dictionary<ulong, string>();
        var imports = new Dictionary<string, ulong[]>(StringComparer.OrdinalIgnoreCase);
        var graph = new Dictionary<string, HashSet<string>>(StringComparer.OrdinalIgnoreCase);
        var row = new Regex(@"^\S+\s+[0-9a-f]+\s+(\d+)\s+ExportBundleData\s+(\S+)\s+.*imported_packages: \[(.*?)\]", RegexOptions.Compiled);
        var id = new Regex(@"FPackageId\((\d+)\)", RegexOptions.Compiled);
        foreach (string line in File.ReadLines(store))
        {
            var m = row.Match(line);
            if (!m.Success) continue;
            string storedPath = m.Groups[2].Value.Replace('\\', '/');
            int contentOffset = storedPath.IndexOf("/AION2/Content/", StringComparison.Ordinal);
            if (contentOffset < 0 && !actions) continue;
            string path = contentOffset >= 0 ? "/Game/" + Path.ChangeExtension(storedPath.Substring(contentOffset + 15), null) :
                "/ExternalPackage/" + m.Groups[1].Value;
            ids[ulong.Parse(m.Groups[1].Value)] = path;
            imports[path] = id.Matches(m.Groups[3].Value).Select(x => ulong.Parse(x.Groups[1].Value)).ToArray();
        }
        manifest.StorePackages = ids.Count;
        if (ids.Count == 0) throw new InvalidDataException("No package store rows decoded");
        foreach (var entry in imports)
        {
            var links = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
            foreach (ulong reference in entry.Value) if (ids.TryGetValue(reference, out string path)) links.Add(path);
            graph[entry.Key] = links;
        }
        imports.Clear(); ids.Clear();
        string content = Path.Combine(root, "AION2", "Content");
        var candidates = new List<string>();
        foreach (string file in Directory.EnumerateFiles(content, "*", SearchOption.AllDirectories).Where(f =>
            Path.GetExtension(f).Equals(".uasset", StringComparison.OrdinalIgnoreCase) || Path.GetExtension(f).Equals(".umap", StringComparison.OrdinalIgnoreCase)))
        {
            string package = "/Game/" + Path.ChangeExtension(Path.GetRelativePath(content, file), null).Replace('\\', '/');
            if (!graph.TryGetValue(package, out var links)) graph[package] = links = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
            try
            {
                // Header-only reading avoids deserializing every cooked script just to find references.
                var asset = new UAsset(file, false, EngineVersion.VER_UE5_3);
                foreach (var name in asset.GetNameMapIndexList())
                {
                    string value = name.ToString();
                    if (!value.StartsWith("/Game/", StringComparison.Ordinal)) continue;
                    int dot = value.IndexOf('.');
                    if (dot >= 0) value = value.Substring(0, dot);
                    if (!value.Equals(package, StringComparison.OrdinalIgnoreCase)) links.Add(value);
                }
                manifest.HeadersRead++;
                if (actions ? PlayerActionBuild.IsCandidate(package) : SelectionPolicy.IsCandidate(package)) candidates.Add(package);
            }
            catch (Exception ex) { manifest.HeaderFailures.Add(package + ": " + ex.Message); }
            if ((manifest.HeadersRead + manifest.HeaderFailures.Count) % 10000 == 0)
                Console.WriteLine($"Headers {manifest.HeadersRead}; failed {manifest.HeaderFailures.Count}");
        }
        if (manifest.HeaderFailures.Count > 0)
        {
            JsonFile(output, manifest);
            throw new InvalidDataException("Incomplete header audit; no assets approved");
        }
        var blocked = SelectionPolicy.ProtectedClosure(graph);
        if (actions) manifest.ActionExclusions = PlayerActionBuild.Exclusions(graph, candidates);
        foreach (string package in candidates.OrderBy(x => x, StringComparer.Ordinal))
        {
            if (blocked.Contains(package) || manifest.ActionExclusions.ContainsKey(package)) { manifest.BlockedByReferences.Add(package); continue; }
            string file = SelectionPolicy.SourceFile(root, package);
            string uexp = Path.ChangeExtension(file, ".uexp");
            if (!File.Exists(uexp)) throw new InvalidDataException("Missing export payload: " + package);
            manifest.Approved.Add(new SelectedAsset { Package = package, UassetSha256 = Hash(file), UexpSha256 = Hash(uexp) });
        }
        JsonFile(output, manifest);
        Console.WriteLine($"Approved {manifest.Approved.Count}; protected references {manifest.BlockedByReferences.Count}; headers {manifest.HeadersRead}");
    }
    static List<StructPropertyData> Handles(UAsset asset)
    {
        var result = new List<StructPropertyData>();
        foreach (var export in asset.Exports.OfType<NormalExport>())
            foreach (var value in export.Data)
                if (value.Name.ToString() == "EmitterHandles" && value is ArrayPropertyData array)
                    foreach (var element in array.Value)
                    {
                        if (!(element is StructPropertyData handle)) throw new InvalidDataException("Unknown emitter handle shape");
                        result.Add(handle);
                    }
        return result;
    }
    static bool AllDisabled(List<StructPropertyData> handles) => handles.Count > 0 && handles.All(h =>
        h.Value.OfType<BoolPropertyData>().Any(v => v.Name.ToString() == "bIsEnabled" && !v.Value));
    static bool SamePair(string left, string right) => Hash(left) == Hash(right) && Hash(Path.ChangeExtension(left, ".uexp")) == Hash(Path.ChangeExtension(right, ".uexp"));
    static void Build(string usmap, string manifestPath, string output, string report)
    {
        var manifest = JsonConvert.DeserializeObject<SelectionManifest>(File.ReadAllText(manifestPath));
        if (manifest == null || manifest.Kind != "niagara" || manifest.SchemaVersion != 1 || manifest.HeaderFailures.Count != 0 || manifest.HeadersRead == 0 || manifest.StorePackages == 0)
            throw new InvalidDataException("Missing or incomplete reference audit");
        output = DirectoryPath(output);
        string source = DirectoryPath(manifest.SourceRoot);
        report = Path.GetFullPath(report);
        string checkRoot = output + "-roundtrip";
        if (Directory.Exists(output) || File.Exists(report) || Within(output, source) || Within(source, output))
            throw new IOException("Output must be new and separate from source");
        if (Within(report, source) || Within(report, output) || Within(report, checkRoot))
            throw new IOException("Report must be separate from source, output, and roundtrip trees");
        if (Directory.Exists(checkRoot)) throw new IOException("Roundtrip output already exists");
        var unique = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (var item in manifest.Approved)
        {
            if (!SelectionPolicy.IsCandidate(item.Package) || !unique.Add(item.Package)) throw new InvalidDataException("Invalid or duplicate selection");
            string file = SelectionPolicy.SourceFile(source, item.Package);
            if (Hash(file) != item.UassetSha256 || Hash(Path.ChangeExtension(file, ".uexp")) != item.UexpSha256)
                throw new InvalidDataException("Source changed after audit: " + item.Package);
        }
        var mappings = new Usmap(usmap);
        Directory.CreateDirectory(output);
        Directory.CreateDirectory(checkRoot);
        var results = new List<object>();
        int written = 0, emitters = 0;
        foreach (var item in manifest.Approved)
        {
            string file = SelectionPolicy.SourceFile(source, item.Package);
            string destination = SelectionPolicy.SourceFile(output, item.Package);
            string reason = null;
            int count = 0;
            try
            {
                var asset = new UAsset(file, EngineVersion.VER_UE5_3, mappings);
                if (File.Exists(Path.ChangeExtension(file, ".ubulk")) || File.Exists(Path.ChangeExtension(file, ".uptnl"))) reason = "unsupported-bulk-sidecar";
                else if (asset.Exports.Any(e => e is RawExport)) reason = "raw-export";
                else if (!asset.Exports.Any(e => e.OuterIndex.Index == 0 && e.ClassIndex.Index < 0 &&
                    asset.Imports[-e.ClassIndex.Index - 1].ObjectName.ToString() == "NiagaraSystem")) reason = "not-niagara-system";
                else if (asset.GetNameMapIndexList().Any(n => n.ToString().StartsWith("/Game/") && SelectionPolicy.HasGameplayReference(n.ToString()))) reason = "gameplay-reference-name";
                else if (asset.Imports.Any(i => i.ObjectName.ToString() == "NiagaraComponentRendererProperties")) reason = "component-renderer";
                var handles = reason == null ? Handles(asset) : new List<StructPropertyData>();
                if (reason == null && handles.Count == 0) reason = "no-emitter-handles";
                if (reason == null)
                {
                    string roundtrip = Path.Combine(checkRoot, Path.GetFileName(file));
                    asset.Write(roundtrip);
                    if (!SamePair(file, roundtrip)) reason = "non-exact-roundtrip";
                }
                if (reason == null)
                {
                    foreach (var handle in handles)
                    {
                        var enabled = handle.Value.OfType<BoolPropertyData>().SingleOrDefault(v => v.Name.ToString() == "bIsEnabled");
                        if (enabled == null) handle.Value.Add(new BoolPropertyData(new FName(asset, "bIsEnabled")) { Value = false });
                        else enabled.Value = false;
                    }
                    Directory.CreateDirectory(Path.GetDirectoryName(destination));
                    asset.Write(destination);
                    var verify = new UAsset(destination, EngineVersion.VER_UE5_3, mappings);
                    if (verify.Exports.Any(e => e is RawExport) || Handles(verify).Count != handles.Count || !AllDisabled(Handles(verify)))
                        throw new InvalidDataException("Emitter read-back validation failed");
                    count = handles.Count;
                    written++; emitters += count;
                }
            }
            catch (Exception ex)
            {
                reason = "exception: " + ex.Message;
                if (File.Exists(destination)) File.Delete(destination);
                if (File.Exists(Path.ChangeExtension(destination, ".uexp"))) File.Delete(Path.ChangeExtension(destination, ".uexp"));
            }
            results.Add(new { item.Package, Status = reason == null ? "written" : "skipped", Reason = reason, Emitters = count });
            if (results.Count % 100 == 0) { Console.WriteLine($"Checked {results.Count}/{manifest.Approved.Count}, written {written}"); GC.Collect(); }
        }
        JsonFile(report, new { SourceRoot = source, OutputRoot = output, AuditSha256 = Hash(manifestPath), Approved = manifest.Approved.Count,
            Written = written, Emitters = emitters, Skipped = results.Count - written, Results = results,
            Limitations = manifest.Limitations, Installed = false });
        if (written == 0) throw new InvalidDataException("No assets passed; no pak should be built");
        Console.WriteLine($"Written {written} verified assets; disabled {emitters} emitters; skipped {results.Count - written}");
    }
}
