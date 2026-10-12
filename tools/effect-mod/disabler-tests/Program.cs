using System.Reflection;
using System.Security.Cryptography;
using System.Text.Json;
using UAssetAPI;
using UAssetAPI.ExportTypes;
using UAssetAPI.PropertyTypes.Objects;
using UAssetAPI.PropertyTypes.Structs;
using UAssetAPI.UnrealTypes;
using UAssetAPI.Unversioned;

int failures = 0, checks = 0;
void Check(bool value, string message)
{
    checks++;
    if (!value) { failures++; Console.WriteLine("FAIL: " + message); }
}
var policy = Assembly.Load("disabler").GetType("SelectionPolicy");
Check(policy != null, "selective build must expose the safety filter instead of disabling an entire tree");
if (policy == null) return 1;
bool Candidate(string path) => (bool)policy.GetMethod("IsCandidate").Invoke(null, new object[] { path });
bool Protected(string path) => (bool)policy.GetMethod("IsProtectedRoot").Invoke(null, new object[] { path });

// These cases catch accidentally broadening a folder match or dropping a protected category.
var cases = new (string path, bool allow)[] {
    ("/Game/FX/Particle/PC/Male/Sorcerer/MagicBook/Skill006/P_Skill006_Cast_001", true),
    ("/Game/FX/Particle/PC/Male/Ranger/Bow/Skill006/P_Ranger_Skill006_Cast_001", true),
    ("/Game/FX/Particle/PC/Male/Ranger/Bow/Skill006/P_Ranger_Skill006_Range_001", false),
    ("/Game/FX/Particle/PC/Male/Sorcerer/MagicBook/Skill006/MI_Skill006_Cast_001", false),
    ("/Game/FX/Particle/PC/Male/Cleric/Mace/Skill004/P_Skill004_Cast_001", false),
    ("/Game/FX/Particle/PC/Male/Templar/Sword/Skill004/P_Skill004_Cast_001", false),
    ("/Game/FX/Particle/PC/Male/Chanter/Staff/Skill004/P_Skill004_Cast_001", false),
    ("/Game/FX/Particle/PC/Common/Abnormal/P_Cast_001", false),
    ("/Game/FX/Particle/Common/AB_Common_TargetMark/P_Cast_001", false),
    ("/Game/FX/Particle/Common/Item_Enchant/P_Item_Enchant_Glow_007", true),
    ("/Game/FX/Particle/Common/Item_Enchant/../AB_Common_TargetMark/P_Cast_001", false),
    ("/Game/FX/Particle/Common/Wing/Wing001/P_Wing001", true),
    ("/Game/FX/Particle/Common/Halo/Halo001/P_Halo001", true),
    ("/Game/FX/Particle/VEH/Bike/P_SignatureFX_001", true),
    ("/Game/FX/Particle/VEH/Bike/P_Bike_Mesh", false),
    ("/Game/FX/Particle/Monster/Boss/P_Smoke_001", false),
    ("/Game/FX/Particle/Map/Dungeon/P_Boss_Phase02", false),
    ("/Game/FX/Particle/Module/Smoke/P_Smoke_001", false),
    ("/Game/FX/Particle/FO/Env/P_Smoke_001", false),
    ("/Game/FX/Particle/PC/Male/Sorcerer/MagicBook/P_Heal_001", false),
    ("/Game/FX/Particle/PC/Male/Sorcerer/MagicBook/P_Cast_Decal_001", false),
    ("/Game/FX/Particle/PC/Male/Sorcerer/MagicBook/P_Barrier_001", false),
};
foreach (var item in cases) Check(Candidate(item.path) == item.allow, "candidate filter: " + item.path);
foreach (var path in new[] {
    "/Game/Act/Monster/Boss/Atk001", "/Game/FX/Act/Monster/Boss/Atk001",
    "/Game/FX/Particle/Monster/Boss/P_Atk", "/Game/FX/Act/NPC/Thing",
    "/Game/FX/Act/EnvFX/Smoke/EnvFX_Smoke_001_Fx", "/Game/Map/Dungeon/Thing",
    "/Game/FX/Particle/PC/Common/Abnormal/P_Thing", "/Game/FX/Act/Player/Cleric/Skill004"
}) Check(Protected(path), "protect gameplay reference root: " + path);

var graph = new Dictionary<string, HashSet<string>>(StringComparer.OrdinalIgnoreCase) {
    ["/Game/FX/Act/Monster/Boss/Atk001"] = new() { "/Game/FX/Act/Common/Intermediate" },
    ["/Game/FX/Act/Common/Intermediate"] = new() { "/Game/FX/Particle/Common/Wing/P_Shared", "/Game/FX/Act/Monster/Boss/Atk001" },
    ["/Game/FX/Act/Player/Sorcerer/Skill001"] = new() { "/Game/FX/Particle/PC/Male/Sorcerer/P_Exclusive" },
};
var blocked = (HashSet<string>)policy.GetMethod("ProtectedClosure").Invoke(null, new object[] { graph });
Check(blocked.Contains("/Game/FX/Particle/Common/Wing/P_Shared"), "transitive boss dependency must be blocked, even with a cosmetic path");
Check(!blocked.Contains("/Game/FX/Particle/PC/Male/Sorcerer/P_Exclusive"), "an exclusive offensive effect should not be rejected as a boss dependency");
Check(blocked.Count == 3, "cycle traversal must terminate without adding unrelated assets");
if (args.Length == 2)
{
    string testRoot = Path.Combine(Path.GetTempPath(), "aion2-selective-tests-" + Guid.NewGuid().ToString("N"));
    string source = Path.Combine(testRoot, "source");
    string prefix = "FX/Particle/PC/Male/Sorcerer/MagicBook/";
    string valid = prefix + "Male_Sorcerer_MagicBook_Normal/P_MB_NormalAtk_Ribbon_001";
    string shared = prefix + "Male_Sorcerer_MagicBook_Passive003/P_Sorcerer_Passive003_Hit_001";
    string raw = prefix + "Male_Sorcerer_MagicBook_Skill030/P_Sorcerer_Skill030_Cast_002";
    string Asset(string root, string path) => Path.Combine(root, "AION2", "Content", path.Replace('/', Path.DirectorySeparatorChar) + ".uasset");
    foreach (string path in new[] { valid, shared, raw })
    {
        string original = Asset(args[0], path), copy = Asset(source, path);
        Directory.CreateDirectory(Path.GetDirectoryName(copy));
        File.Copy(original, copy);
        File.Copy(Path.ChangeExtension(original, ".uexp"), Path.ChangeExtension(copy, ".uexp"));
    }
    var before = Directory.GetFiles(source, "*", SearchOption.AllDirectories).ToDictionary(x => x, x => Convert.ToHexString(SHA256.HashData(File.ReadAllBytes(x))));
    string store = Path.Combine(testRoot, "store.txt");
    string Row(int id, string path, string links) => $"fixture 000000000000000000000001 {id} ExportBundleData ../../../AION2/Content/{path}.uasset \"StoreEntry {{ imported_packages: [{links}] }}\"";
    File.WriteAllLines(store, new[] { Row(1, valid, ""), Row(2, shared, ""), Row(3, raw, ""), Row(4, "FX/Act/Monster/Boss/Atk001", "FPackageId(2)") });
    int Run(params string[] command) => (int)Assembly.Load("disabler").GetType("SelectiveBuild").GetMethod("Run").Invoke(null, new object[] { command });
    string manifest = Path.Combine(testRoot, "selection.json");
    Check(Run("--audit", source, store, manifest) == 0, "real header audit should succeed");
    using (var audit = JsonDocument.Parse(File.ReadAllText(manifest)))
    {
        Check(audit.RootElement.GetProperty("Approved").GetArrayLength() == 2, "boss-shared asset must be excluded from the real audit");
        Check(audit.RootElement.GetProperty("BlockedByReferences")[0].GetString() == "/Game/" + shared, "audit must identify the exact boss-shared asset");
    }
    string output = Path.Combine(testRoot, "modified"), report = Path.Combine(testRoot, "report.json");
    Check(Run("--selective", args[1], manifest, output, report) == 0, "known-good Niagara asset should survive roundtrip and build");
    using (var built = JsonDocument.Parse(File.ReadAllText(report)))
    {
        Check(built.RootElement.GetProperty("Written").GetInt32() == 1, "only the valid unshared effect should be written");
        Check(built.RootElement.GetProperty("Skipped").GetInt32() == 1, "opaque RawExport must be explicitly skipped");
        Check(built.RootElement.GetProperty("Results").EnumerateArray().Any(x => x.GetProperty("Reason").GetString() == "raw-export"), "report must not hide raw exports as zero errors");
    }
    string changed = Asset(output, valid);
    Check(File.Exists(changed) && !File.Exists(Asset(output, shared)) && !File.Exists(Asset(output, raw)), "protected and unreadable assets must never enter the output");
    if (File.Exists(changed))
    {
        var asset = new UAsset(changed, EngineVersion.VER_UE5_3, new Usmap(args[1]));
        var handles = asset.Exports.OfType<NormalExport>().SelectMany(e => e.Data).OfType<ArrayPropertyData>()
            .Where(p => p.Name.ToString() == "EmitterHandles").SelectMany(p => p.Value).OfType<StructPropertyData>().ToArray();
        Check(handles.Length == 1 && handles.All(h => h.Value.OfType<BoolPropertyData>().Any(v => v.Name.ToString() == "bIsEnabled" && !v.Value)), "output must actually disable the fixture emitter");
    }
    Check(before.All(x => Convert.ToHexString(SHA256.HashData(File.ReadAllBytes(x.Key))) == x.Value), "all baseline bytes must remain unchanged");
    Check(Run("--selective", args[1], manifest, source, Path.Combine(testRoot, "bad-report.json")) != 0, "builder must reject in-place modification");
    string trailingManifest = Path.Combine(testRoot, "trailing-selection.json");
    var trailing = System.Text.Json.Nodes.JsonNode.Parse(File.ReadAllText(manifest));
    trailing["SourceRoot"] = source + Path.DirectorySeparatorChar;
    File.WriteAllText(trailingManifest, trailing.ToJsonString());
    string nested = Path.Combine(source, "nested-output");
    Check(Run("--selective", args[1], trailingManifest, nested, Path.Combine(testRoot, "nested-report.json")) != 0,
        "a trailing source separator must not allow nested output");
    Check(!Directory.Exists(nested), "nested output must fail before any creation");
    string suffixOutput = Path.Combine(testRoot, "suffix-output");
    Check(Run("--selective", args[1], manifest, suffixOutput + Path.DirectorySeparatorChar, Path.Combine(testRoot, "suffix-report.json")) == 0,
        "a trailing output separator should be normalized");
    Check(Directory.Exists(suffixOutput + "-roundtrip") && !Directory.Exists(Path.Combine(suffixOutput, "-roundtrip")),
        "roundtrip baselines must be siblings, never part of the cook input");
    string overlapOutput = Path.Combine(testRoot, "overlap-output");
    Check(Run("--selective", args[1], manifest, overlapOutput, Asset(overlapOutput, valid)) != 0,
        "report must not overwrite a generated asset");
    Check(!Directory.Exists(overlapOutput), "overlapping report must fail before output creation");
    Check(Run("--selective", args[1], manifest, Path.Combine(testRoot, "source-report-output"), Path.Combine(source, "report.json")) != 0,
        "report must not write into the baseline tree");

    // A map with a name-table-only reference must protect its player/cosmetic dependency.
    string mapRoot = Path.Combine(testRoot, "map-source");
    string mapSource = Asset(mapRoot, valid);
    Directory.CreateDirectory(Path.GetDirectoryName(mapSource));
    File.Copy(Asset(args[0], valid), mapSource);
    File.Copy(Path.ChangeExtension(Asset(args[0], valid), ".uexp"), Path.ChangeExtension(mapSource, ".uexp"));
    string mapPackage = "Map/Dungeon/HeaderOnlyFixture";
    string mapFile = Path.ChangeExtension(Asset(mapRoot, mapPackage), ".umap");
    Directory.CreateDirectory(Path.GetDirectoryName(mapFile));
    var mapFixture = new UAsset(Asset(args[0], valid), EngineVersion.VER_UE5_3, new Usmap(args[1]));
    _ = new FName(mapFixture, "/Game/" + valid);
    mapFixture.Write(mapFile);
    string mapStore = Path.Combine(testRoot, "map-store.txt"), mapManifest = Path.Combine(testRoot, "map-selection.json");
    File.WriteAllLines(mapStore, new[] { Row(1, valid, ""), Row(5, mapPackage, "").Replace(".uasset", ".umap") });
    Check(Run("--audit", mapRoot, mapStore, mapManifest) == 0, "map fixture audit should succeed");
    using (var mapAudit = JsonDocument.Parse(File.ReadAllText(mapManifest)))
    {
        Check(mapAudit.RootElement.GetProperty("HeadersRead").GetInt32() == 2, "both uasset and umap headers must be read");
        Check(mapAudit.RootElement.GetProperty("Approved").GetArrayLength() == 0 &&
            mapAudit.RootElement.GetProperty("BlockedByReferences")[0].GetString() == "/Game/" + valid,
            "a protected map header's soft reference must prevent disabling its dependency");
    }
    File.AppendAllText(Asset(source, valid), "source changed after audit");
    string tamperedOutput = Path.Combine(testRoot, "tampered-output");
    Check(Run("--selective", args[1], manifest, tamperedOutput, Path.Combine(testRoot, "tampered-report.json")) != 0, "changed baseline must fail hash preflight");
    Check(!Directory.Exists(tamperedOutput), "a failed preflight must not create an output tree");
    Console.WriteLine("Integration evidence: " + testRoot);
}
ActionTests.Run(args, Check);
Console.WriteLine($"{checks} checks, {failures} failures");
return failures == 0 ? 0 : 1;
